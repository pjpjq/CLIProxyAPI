package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers"
)

func TestOpenAIModels_EnforcesOwnedByOpenAI(t *testing.T) {
	gin.SetMode(gin.TestMode)

	clientID := "test-enforce-owned-by-client"
	modelRegistry := registry.GetGlobalRegistry()
	modelRegistry.RegisterClient(clientID, "openai-compatibility", []*registry.ModelInfo{
		{ID: "model-groq-provider", OwnedBy: "groq"},
		{ID: "model-anthropic-provider", OwnedBy: "anthropic"},
		{ID: "model-without-owned-by", OwnedBy: ""},
	})
	t.Cleanup(func() {
		modelRegistry.UnregisterClient(clientID)
	})

	base := handlers.NewBaseAPIHandlers(&config.SDKConfig{}, nil)
	handler := NewOpenAIAPIHandler(base)

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)

	handler.OpenAIModels(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("OpenAIModels status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Object string           `json:"object"`
		Data   []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Object != "list" {
		t.Fatalf("object = %q, want 'list'", resp.Object)
	}

	// 1. Verify that all returned models in the response have owned_by == "openai"
	for _, m := range resp.Data {
		id, _ := m["id"].(string)
		ownedBy, exists := m["owned_by"].(string)
		if !exists {
			t.Fatalf("model %q missing owned_by field in response: %#v", id, m)
		}
		if ownedBy != "openai" {
			t.Fatalf("model %q owned_by = %q, want 'openai'", id, ownedBy)
		}
	}

	// 2. Explicitly verify the 3 injected test models are present in the response and owned_by == "openai".
	// Target verification is scoped to these 3 registered models without assuming GlobalRegistry is empty.
	targetModels := map[string]bool{
		"model-groq-provider":      false,
		"model-anthropic-provider": false,
		"model-without-owned-by":   false,
	}

	for _, m := range resp.Data {
		id, _ := m["id"].(string)
		if _, expected := targetModels[id]; expected {
			targetModels[id] = true
			if m["owned_by"] != "openai" {
				t.Fatalf("target model %q owned_by = %v, want 'openai'", id, m["owned_by"])
			}
		}
	}

	for id, found := range targetModels {
		if !found {
			t.Fatalf("expected injected model %q to be present in /v1/models response", id)
		}
	}
}
