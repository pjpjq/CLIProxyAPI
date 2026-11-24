package auth

import (
	"context"
	"net/http"
	"testing"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

func TestDailiCanceledStreamPreservesClassifiedBadGateway(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err        error
		wantFailed int64
	}{
		{"classified upstream error", &Error{HTTPStatus: http.StatusBadGateway, Code: transientTransportErrorCode}, 1},
		{"unclassified bad gateway", &Error{HTTPStatus: http.StatusBadGateway}, 0},
		{"client cancellation", context.Canceled, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := NewManager(nil, nil, nil)
			auth := &Auth{ID: "aistudio-cancel", Provider: "aistudio", Status: StatusActive}
			if _, err := manager.Register(t.Context(), auth); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			const model = "gemini-4-argon"
			stream := manager.wrapStreamResult(ctx, auth, auth.Provider, model, model, nil, nil, streamErrorResult(nil, tc.err).Chunks, OAuthModelAliasResult{}, false, cliproxyexecutor.Options{}, 0)
			for range stream.Chunks {
			}
			got, ok := manager.GetByID(auth.ID)
			if !ok || got.Failed != tc.wantFailed || got.Unavailable {
				t.Fatalf("auth = %+v, want %d failures and no cooldown", got, tc.wantFailed)
			}
			if state := got.ModelStates[model]; state != nil && state.Unavailable {
				t.Fatal("upstream transport failure cooled the model")
			}
		})
	}
}
