package executor

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/redisqueue"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

func passthroughTestAuth(baseURL string) *cliproxyauth.Auth {
	return &cliproxyauth.Auth{Attributes: map[string]string{"api_key": "test-key", "base_url": baseURL}}
}

func passthroughTestOptions(stream bool) cliproxyexecutor.Options {
	return cliproxyexecutor.Options{
		Stream: stream, SourceFormat: sdktranslator.FormatOpenAIResponse,
		ResponseFormat: sdktranslator.FormatOpenAIResponse,
		Metadata:       map[string]any{cliproxyexecutor.RequestPathMetadataKey: "/v1/responses"},
	}
}

func TestCodexResponsesPassThroughPreservesBodyAndResponse(t *testing.T) {
	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Errorf("path = %s, want /responses", r.URL.Path)
		}
		got, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_upstream","output":[{"type":"function_call","call_id":"call_keep"}],"unknown_future":{"x":1}}`))
	}))
	defer srv.Close()

	cfg := &config.Config{CodexKey: []config.CodexKey{{APIKey: "test-key", BaseURL: srv.URL, ResponsesPassThrough: true}}}
	e := NewCodexExecutor(cfg)
	body := []byte(`{"model":"client-alias","previous_response_id":"resp_prev","reasoning":{"encrypted_content":"opaque"},"input":[{"type":"function_call_output","call_id":"call_keep","output":"ok"}],"unknown_future":{"x":1}}`)
	resp, err := e.Execute(t.Context(), passthroughTestAuth(srv.URL), cliproxyexecutor.Request{Model: "upstream-model", Payload: body}, passthroughTestOptions(false))
	if err != nil {
		t.Fatal(err)
	}
	var sent map[string]any
	if err := json.Unmarshal(got, &sent); err != nil {
		t.Fatal(err)
	}
	if sent["model"] != "upstream-model" || sent["previous_response_id"] != "resp_prev" || sent["reasoning"].(map[string]any)["encrypted_content"] != "opaque" {
		t.Fatalf("body fields not preserved: %s", got)
	}
	if sent["unknown_future"].(map[string]any)["x"] != float64(1) {
		t.Fatalf("unknown field lost: %s", got)
	}
	if !strings.Contains(string(resp.Payload), `"call_id":"call_keep"`) {
		t.Fatalf("response was rebuilt: %s", resp.Payload)
	}
}

func TestCodexResponsesPassThroughStreamIsRaw(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `event: response.output_item.done
data: {"type":"function_call","call_id":"call_keep"}

`)
	}))
	defer srv.Close()
	cfg := &config.Config{CodexKey: []config.CodexKey{{APIKey: "test-key", BaseURL: srv.URL, ResponsesPassThrough: true}}}
	result, err := NewCodexExecutor(cfg).ExecuteStream(t.Context(), passthroughTestAuth(srv.URL), cliproxyexecutor.Request{Model: "model", Payload: []byte(`{"model":"model"}`)}, passthroughTestOptions(true))
	if err != nil {
		t.Fatal(err)
	}
	var all []byte
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
		}
		all = append(all, chunk.Payload...)
	}
	if !strings.Contains(string(all), `call_keep`) || !strings.Contains(string(all), "event: response.output_item.done") {
		t.Fatalf("SSE was not raw: %q", all)
	}
}

func TestCodexResponsesPassThroughDisabledByDefault(t *testing.T) {
	e := NewCodexExecutor(&config.Config{CodexKey: []config.CodexKey{{APIKey: "k", BaseURL: "http://invalid", ResponsesPassThrough: false}}})
	if e.responsesPassThroughEnabled(passthroughTestAuth("http://invalid"), passthroughTestOptions(false)) {
		t.Fatal("pass-through enabled when disabled")
	}
}

func TestCodexResponsesPassThroughRecordsUsageStream(t *testing.T) {
	prevQueueEnabled := redisqueue.Enabled()
	prevUsageEnabled := redisqueue.UsageStatisticsEnabled()
	redisqueue.SetEnabled(false)
	redisqueue.SetEnabled(true)
	redisqueue.SetUsageStatisticsEnabled(true)
	t.Cleanup(func() {
		redisqueue.SetEnabled(false)
		redisqueue.SetEnabled(prevQueueEnabled)
		redisqueue.SetUsageStatisticsEnabled(prevUsageEnabled)
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.output_item.done\n"+
			"data: {\"type\":\"function_call\",\"call_id\":\"call_keep\"}\n\n"+
			"event: response.completed\n"+
			"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":42,\"output_tokens\":10,\"total_tokens\":52}}}\n\n")
	}))
	defer srv.Close()

	cfg := &config.Config{CodexKey: []config.CodexKey{{APIKey: "test-key", BaseURL: srv.URL, ResponsesPassThrough: true}}}
	result, err := NewCodexExecutor(cfg).ExecuteStream(t.Context(), passthroughTestAuth(srv.URL), cliproxyexecutor.Request{Model: "gpt-5.6-sol", Payload: []byte(`{"model":"gpt-5.6-sol"}`)}, passthroughTestOptions(true))
	if err != nil {
		t.Fatal(err)
	}
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
		}
	}

	var foundItem []byte
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		items := redisqueue.PopOldest(10)
		if len(items) > 0 {
			foundItem = items[len(items)-1]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(foundItem) == 0 {
		t.Fatal("expected usage record in queue, got 0")
	}
	var record map[string]any
	if err := json.Unmarshal(foundItem, &record); err != nil {
		t.Fatal(err)
	}
	if record["model"] != "gpt-5.6-sol" {
		t.Fatalf("expected model gpt-5.6-sol, got %v", record["model"])
	}
	tokens, ok := record["tokens"].(map[string]any)
	if !ok {
		t.Fatalf("missing tokens in record: %+v", record)
	}
	if tokens["input_tokens"] != float64(42) || tokens["output_tokens"] != float64(10) || tokens["total_tokens"] != float64(52) {
		t.Fatalf("unexpected token usage: %+v", tokens)
	}
}

func TestCodexResponsesPassThroughRecordsUsageNonStream(t *testing.T) {
	prevQueueEnabled := redisqueue.Enabled()
	prevUsageEnabled := redisqueue.UsageStatisticsEnabled()
	redisqueue.SetEnabled(false)
	redisqueue.SetEnabled(true)
	redisqueue.SetUsageStatisticsEnabled(true)
	t.Cleanup(func() {
		redisqueue.SetEnabled(false)
		redisqueue.SetEnabled(prevQueueEnabled)
		redisqueue.SetUsageStatisticsEnabled(prevUsageEnabled)
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_123","output":[],"usage":{"input_tokens":30,"output_tokens":15,"total_tokens":45}}`)
	}))
	defer srv.Close()

	cfg := &config.Config{CodexKey: []config.CodexKey{{APIKey: "test-key", BaseURL: srv.URL, ResponsesPassThrough: true}}}
	_, err := NewCodexExecutor(cfg).Execute(t.Context(), passthroughTestAuth(srv.URL), cliproxyexecutor.Request{Model: "gpt-5.6-sol", Payload: []byte(`{"model":"gpt-5.6-sol"}`)}, passthroughTestOptions(false))
	if err != nil {
		t.Fatal(err)
	}

	var foundItem []byte
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		items := redisqueue.PopOldest(10)
		if len(items) > 0 {
			foundItem = items[len(items)-1]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(foundItem) == 0 {
		t.Fatal("expected usage record in queue, got 0")
	}
	var record map[string]any
	if err := json.Unmarshal(foundItem, &record); err != nil {
		t.Fatal(err)
	}
	if record["model"] != "gpt-5.6-sol" {
		t.Fatalf("expected model gpt-5.6-sol, got %v", record["model"])
	}
	tokens, ok := record["tokens"].(map[string]any)
	if !ok {
		t.Fatalf("missing tokens in record: %+v", record)
	}
	if tokens["input_tokens"] != float64(30) || tokens["output_tokens"] != float64(15) || tokens["total_tokens"] != float64(45) {
		t.Fatalf("unexpected token usage: %+v", tokens)
	}
}
