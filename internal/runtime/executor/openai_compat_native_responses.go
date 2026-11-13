package executor

import (
	"net/http"
	"strings"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
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
