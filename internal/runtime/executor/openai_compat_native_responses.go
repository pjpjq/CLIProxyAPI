package executor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	log "github.com/sirupsen/logrus"
)

// Native Responses wire helpers live here to keep openai_compat_executor.go close to upstream.

// RequestToFormat reports the upstream request format used after auth selection.
func (e *OpenAICompatExecutor) RequestToFormat(_ cliproxyexecutor.Request, opts cliproxyexecutor.Options) sdktranslator.Format {
	return openAICompatRequestToFormat(false, opts)
}

// RequestToFormatWithAuth reports the upstream request format for the selected credential.
func (e *OpenAICompatExecutor) RequestToFormatWithAuth(auth *cliproxyauth.Auth, _ cliproxyexecutor.Request, opts cliproxyexecutor.Options) sdktranslator.Format {
	return openAICompatRequestToFormat(e.useNativeResponsesWire(auth, opts), opts)
}

func openAICompatRequestToFormat(nativeResponses bool, opts cliproxyexecutor.Options) sdktranslator.Format {
	source := opts.SourceFormat.String()
	if source == openAICompatImageHandlerType || source == "openai-video" {
		return opts.SourceFormat
	}
	if opts.Alt == "responses/compact" {
		if !opts.Stream {
			return sdktranslator.FormatOpenAIResponse
		}
		return sdktranslator.FormatOpenAI
	}
	if nativeResponses {
		return sdktranslator.FormatOpenAIResponse
	}
	return sdktranslator.FormatOpenAI
}

func (e *OpenAICompatExecutor) useNativeResponsesWire(auth *cliproxyauth.Auth, opts cliproxyexecutor.Options) bool {
	if auth == nil || auth.Attributes == nil {
		return false
	}
	return openAICompatUsesNativeResponsesWire(auth.Attributes["wire_api"], opts)
}

func openAICompatUsesNativeResponsesWire(wireAPI string, opts cliproxyexecutor.Options) bool {
	return opts.Alt != "responses/compact" && opts.SourceFormat == sdktranslator.FormatOpenAIResponse && strings.EqualFold(strings.TrimSpace(wireAPI), "responses")
}

type openAICompatIncompleteStreamError struct {
	statusErr
}

func newOpenAICompatIncompleteStreamError() openAICompatIncompleteStreamError {
	return openAICompatIncompleteStreamError{statusErr: statusErr{
		code: http.StatusRequestTimeout,
		msg:  "stream error: stream disconnected before completion: stream closed before response.completed or response.incomplete",
	}}
}

func (openAICompatIncompleteStreamError) IsRequestScoped() bool {
	return true
}

// Take ownership of the successful native response without patching the ordinary stream loop.
func (e *OpenAICompatExecutor) streamNativeResponses(ctx context.Context, httpResp *http.Response, req cliproxyexecutor.Request, opts cliproxyexecutor.Options, originalPayload, translated []byte, reporter *helps.UsageReporter) *cliproxyexecutor.StreamResult {
	out := make(chan cliproxyexecutor.StreamChunk)
	go func() {
		defer close(out)
		defer func() {
			if errClose := httpResp.Body.Close(); errClose != nil {
				log.Errorf("openai compat executor: close response body error: %v", errClose)
			}
		}()
		scanner := bufio.NewScanner(httpResp.Body)
		scanner.Buffer(nil, 52_428_800) // 50MB
		wire := sdktranslator.FormatOpenAIResponse
		responseFormat := cliproxyexecutor.ResponseFormatOrSource(opts)
		original := helps.ApplyPatchOriginalRequest(req, opts)
		claudeInputTokens := helps.NewClaudeInputTokenState(opts.SourceFormat, wire, responseFormat, originalPayload)
		var param any
		helps.InitializeApplyPatchStream(ctx, wire, responseFormat, req.Model, original, translated, &param)
		var streamUsage helps.StreamUsageBuffer
		var nativeTerminalSeen, terminalDelivered, streamFailed, streamAborted bool
		defer func() {
			if errCtx := ctx.Err(); (streamAborted || errCtx != nil) && !terminalDelivered && !streamFailed {
				if errCtx == nil {
					errCtx = context.Canceled
				}
				if !streamUsage.PublishFailure(ctx, reporter, errCtx) {
					reporter.PublishFailure(ctx, errCtx)
				}
				return
			}
			streamUsage.Publish(ctx, reporter)
		}()

		applyPatchErr := statusErr{code: http.StatusBadGateway, msg: helps.ApplyPatchUpstreamErrorMessage}
		sendChunks := func(chunks [][]byte) bool {
			for _, chunk := range chunks {
				select {
				case out <- cliproxyexecutor.StreamChunk{Payload: chunk}:
				case <-ctx.Done():
					streamAborted = true
					return false
				}
			}
			return true
		}
		publishStreamError := func(streamErr, loggedErr error) {
			if loggedErr == nil {
				loggedErr = streamErr
			}
			helps.RecordAPIResponseError(ctx, e.cfg, loggedErr)
			if !streamUsage.PublishFailure(ctx, reporter, loggedErr) {
				reporter.PublishFailure(ctx, loggedErr)
			}
			streamFailed = true
			select {
			case out <- cliproxyexecutor.StreamChunk{Err: streamErr}:
			case <-ctx.Done():
			}
		}
		publishPayloadError := func(streamErr statusErr) {
			publishStreamError(streamErr, statusErr{code: streamErr.code, msg: "upstream stream returned an error payload"})
		}

		var upstreamEvent string
		var frameData [][]byte
		processFrame := func() bool {
			eventName, dataLines := upstreamEvent, frameData
			upstreamEvent, frameData = "", nil
			if len(dataLines) == 0 {
				if openAICompatErrorEvent(eventName) {
					publishStreamError(statusErr{code: http.StatusBadGateway, msg: "upstream error event ended without data"}, nil)
					return true
				}
				return false
			}
			if len(dataLines) > 1 {
				for _, dataLine := range dataLines {
					if bytes.Equal(bytes.TrimSpace(dataLine), []byte("[DONE]")) {
						publishStreamError(statusErr{code: http.StatusBadGateway, msg: "upstream stream ended with incomplete data before [DONE]"}, nil)
						return true
					}
				}
			}
			dataPayload := bytes.TrimSpace(bytes.Join(dataLines, []byte("\n")))
			isDone := bytes.Equal(dataPayload, []byte("[DONE]"))
			if isDone && openAICompatErrorEvent(eventName) {
				publishStreamError(statusErr{code: http.StatusBadGateway, msg: "upstream error event ended before [DONE]"}, nil)
				return true
			}
			if !isDone {
				if !json.Valid(dataPayload) {
					publishStreamError(statusErr{code: http.StatusBadGateway, msg: "upstream stream ended with incomplete SSE data frame"}, nil)
					return true
				}
				if streamErr, _, ok := codexTerminalFailureErr(dataPayload); ok {
					publishPayloadError(streamErr)
					return true
				}
				if streamErr, isError := openAICompatStreamDataError(dataPayload, eventName); isError {
					publishPayloadError(streamErr)
					return true
				}
				// Observe complete upstream frames before translation or downstream backpressure.
				if len(dataLines) > 1 {
					streamUsage.ObserveOpenAIStream(dataPayload)
				}
			}
			helps.ObserveChatTokenEvent(reporter, dataPayload)
			streamLine := append([]byte("data: "), dataPayload...)
			chunks := helps.TranslateStreamWithClaudeInputTokens(ctx, wire, responseFormat, req.Model, original, translated, streamLine, &param, claudeInputTokens)
			helps.RecordApplyPatchStreamFailure(ctx, param, reporter, applyPatchErr)
			if !sendChunks(chunks) {
				return true
			}
			if helps.ApplyPatchTranslationError(param) != nil {
				publishStreamError(applyPatchErr, nil)
				return true
			}
			switch ojson.GetBytesNoCopy(dataPayload, "type").String() {
			case "response.completed", "response.incomplete":
				nativeTerminalSeen = true
				terminalDelivered = len(chunks) > 0
				return true
			}
			return false
		}

	scanLoop:
		for scanner.Scan() {
			line := scanner.Bytes()
			helps.AppendAPIResponseChunk(ctx, e.cfg, line)
			streamUsage.ObserveOpenAIStream(line)
			trimmed := bytes.TrimSpace(line)
			switch {
			case len(trimmed) == 0:
				if processFrame() {
					break scanLoop
				}
			case bytes.HasPrefix(trimmed, []byte("data:")):
				frameData = append(frameData, bytes.Clone(bytes.TrimSpace(trimmed[len("data:"):])))
			case bytes.HasPrefix(trimmed, []byte("event:")):
				upstreamEvent = strings.TrimSpace(string(trimmed[len("event:"):]))
				fallthrough
			case bytes.HasPrefix(trimmed, []byte(":")), bytes.HasPrefix(trimmed, []byte("id:")), bytes.HasPrefix(trimmed, []byte("retry:")):
				chunks := helps.TranslateStreamWithClaudeInputTokens(ctx, wire, responseFormat, req.Model, opts.OriginalRequest, translated, bytes.Clone(line), &param, claudeInputTokens)
				if !sendChunks(chunks) {
					break scanLoop
				}
			case bytes.HasPrefix(trimmed, []byte("{")), bytes.HasPrefix(trimmed, []byte("[")):
				publishPayloadError(statusErr{code: http.StatusBadGateway, msg: string(trimmed)})
				break scanLoop
			}
		}
		errScan := scanner.Err()
		if errScan == nil && !streamFailed && !streamAborted && len(frameData) > 0 {
			_ = processFrame()
		}
		if streamFailed || streamAborted || helps.EndApplyPatchStream(ctx, param, reporter, out, applyPatchErr) {
			return
		}
		if errScan != nil {
			if terminalDelivered && openAICompatIsContextCancelErr(ctx, errScan) {
				return
			}
			publishStreamError(errScan, nil)
		} else if !nativeTerminalSeen {
			if ctx.Err() == nil {
				publishStreamError(newOpenAICompatIncompleteStreamError(), nil)
			}
			return
		}
		// Ensure we record the request if no usage chunk was ever seen.
		streamUsage.Publish(ctx, reporter)
		reporter.EnsurePublished(ctx)
	}()
	return &cliproxyexecutor.StreamResult{Headers: httpResp.Header.Clone(), Chunks: out}
}
