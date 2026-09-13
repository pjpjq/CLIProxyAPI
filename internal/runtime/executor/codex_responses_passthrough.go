package executor

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// responsesPassThroughEnabled is deliberately provider-scoped. OAuth-backed
// Codex requests and credentials without the opt-in continue through the
// existing translator/executor path unchanged.
func (e *CodexExecutor) responsesPassThroughEnabled(auth *cliproxyauth.Auth, opts cliproxyexecutor.Options) bool {
	if e == nil || e.cfg == nil || opts.Alt == "responses/compact" {
		return false
	}
	if opts.SourceFormat != "" && opts.SourceFormat != sdktranslator.FormatOpenAIResponse {
		return false
	}
	if path, ok := opts.Metadata[cliproxyexecutor.RequestPathMetadataKey].(string); ok &&
		!strings.Contains(path, "/responses") {
		return false
	}
	entry := e.resolveCodexConfig(auth)
	return entry != nil && entry.ResponsesPassThrough
}

func responsesPassThroughBody(req cliproxyexecutor.Request) []byte {
	return bytes.Clone(req.Payload)
}

func responsesPassThroughModel(body []byte, model string) []byte {
	if model == "" {
		return body
	}
	current := gjson.GetBytes(body, "model")
	if !current.Exists() || current.String() == model {
		return body
	}
	if updated, err := sjson.SetBytes(body, "model", model); err == nil {
		return updated
	}
	return body
}

func (e *CodexExecutor) executeResponsesPassThrough(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (resp cliproxyexecutor.Response, err error) {
	baseModel := thinking.ParseSuffix(req.Model).ModelName
	reporter := helps.NewExecutorUsageReporter(ctx, e, baseModel, auth)
	reporter.SetStream(false)
	defer reporter.TrackFailure(ctx, &err)

	body := responsesPassThroughBody(req)
	if len(body) == 0 {
		return cliproxyexecutor.Response{}, statusErr{code: http.StatusBadRequest, msg: "empty Responses request body"}
	}
	body = responsesPassThroughModel(body, req.Model)
	// Resolve the client model alias while keeping every other field opaque.
	if model := gjson.GetBytes(body, "model"); model.Exists() && req.Model != "" && model.String() != req.Model {
		if updated, err := sjson.SetBytes(body, "model", req.Model); err == nil {
			body = updated
		}
	}
	_, baseURL := codexCreds(auth)
	if strings.TrimSpace(baseURL) == "" {
		return cliproxyexecutor.Response{}, statusErr{code: http.StatusUnauthorized, msg: "missing provider baseURL"}
	}
	url := strings.TrimSuffix(baseURL, "/") + "/responses"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return cliproxyexecutor.Response{}, err
	}
	applyCodexHeaders(httpReq, auth, codexCredsKey(auth), false, e.cfg, opts.Headers)
	httpReq.Header.Set("Accept", "application/json")
	client := helps.NewUtlsHTTPClient(ctx, e.cfg, auth, 0)
	httpClient := reporter.TrackHTTPClient(client)
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		return cliproxyexecutor.Response{}, err
	}
	defer httpResp.Body.Close()
	data, readErr := io.ReadAll(httpResp.Body)
	if readErr != nil {
		return cliproxyexecutor.Response{}, readErr
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return cliproxyexecutor.Response{}, newCodexStatusErr(httpResp.StatusCode, data)
	}
	if detail, ok := helps.ParseCodexUsage(data); ok {
		reporter.Publish(ctx, detail)
	} else if detail := helps.ParseOpenAIUsage(data); detail.TotalTokens > 0 {
		reporter.Publish(ctx, detail)
	} else {
		reporter.EnsurePublished(ctx)
	}
	return cliproxyexecutor.Response{Payload: data, Headers: httpResp.Header.Clone()}, nil
}

func (e *CodexExecutor) executeResponsesPassThroughStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (_ *cliproxyexecutor.StreamResult, err error) {
	baseModel := thinking.ParseSuffix(req.Model).ModelName
	reporter := helps.NewExecutorUsageReporter(ctx, e, baseModel, auth)
	reporter.SetStream(true)
	defer reporter.TrackFailure(ctx, &err)

	body := responsesPassThroughBody(req)
	if len(body) == 0 {
		return nil, statusErr{code: http.StatusBadRequest, msg: "empty Responses request body"}
	}
	body = responsesPassThroughModel(body, req.Model)
	if model := gjson.GetBytes(body, "model"); model.Exists() && req.Model != "" && model.String() != req.Model {
		if updated, err := sjson.SetBytes(body, "model", req.Model); err == nil {
			body = updated
		}
	}
	_, baseURL := codexCreds(auth)
	if strings.TrimSpace(baseURL) == "" {
		return nil, statusErr{code: http.StatusUnauthorized, msg: "missing provider baseURL"}
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(baseURL, "/")+"/responses", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	applyCodexHeaders(httpReq, auth, codexCredsKey(auth), true, e.cfg, opts.Headers)
	httpReq.Header.Set("Accept", "text/event-stream")
	client := helps.NewUtlsHTTPClient(ctx, e.cfg, auth, 0)
	httpClient := reporter.TrackHTTPClientRoundTripOnly(client)
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		data, _ := io.ReadAll(httpResp.Body)
		httpResp.Body.Close()
		return nil, newCodexStatusErr(httpResp.StatusCode, data)
	}
	out := make(chan cliproxyexecutor.StreamChunk)
	go func() {
		defer close(out)
		defer httpResp.Body.Close()
		defer reporter.EnsurePublished(ctx)
		r := bufio.NewReader(httpResp.Body)
		var published bool
		for {
			chunk, readErr := r.ReadBytes('\n')
			if len(chunk) > 0 {
				select {
				case out <- cliproxyexecutor.StreamChunk{Payload: chunk}:
				case <-ctx.Done():
					return
				}
				if !published && (bytes.Contains(chunk, []byte("response.completed")) || bytes.Contains(chunk, []byte("response.done")) || bytes.Contains(chunk, []byte("response.incomplete"))) {
					line := bytes.TrimSpace(chunk)
					if bytes.HasPrefix(line, []byte("data:")) {
						data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
						if detail, ok := helps.ParseCodexUsage(data); ok {
							reporter.Publish(ctx, detail)
							published = true
						} else if detail := helps.ParseOpenAIUsage(data); detail.TotalTokens > 0 {
							reporter.Publish(ctx, detail)
							published = true
						}
					}
				}
			}
			if readErr != nil {
				if readErr != io.EOF {
					reporter.PublishFailure(ctx, readErr)
					select {
					case out <- cliproxyexecutor.StreamChunk{Err: readErr}:
					case <-ctx.Done():
					}
				}
				return
			}
		}
	}()
	return &cliproxyexecutor.StreamResult{Headers: httpResp.Header.Clone(), Chunks: out}, nil
}

func codexCredsKey(a *cliproxyauth.Auth) string {
	if a != nil && a.Attributes != nil {
		return a.Attributes["api_key"]
	}
	return ""
}
