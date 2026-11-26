package executor

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	sdkauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/auth"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func TestCodexRefreshWithExplicitFilePathStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credential.json")
	if errWrite := os.WriteFile(path, []byte(`{"type":"codex","access_token":"valid","refresh_token":"explicit-file-grant","expired":"2099-01-01T00:00:00Z"}`), 0o600); errWrite != nil {
		t.Fatal(errWrite)
	}
	calls := 0
	original := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`)), Request: req}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	store := sdkauth.NewFileTokenStore()
	manager := cliproxyauth.NewManager(store, nil, nil)
	manager.RegisterExecutor(NewCodexExecutor(&config.Config{AuthDir: dir}))
	auth := &cliproxyauth.Auth{ID: path, FileName: path, Provider: "codex", Status: cliproxyauth.StatusActive, Attributes: map[string]string{cliproxyauth.AttributePath: path}, Metadata: map[string]any{"access_token": "valid", "refresh_token": "explicit-file-grant", "expired": "2099-01-01T00:00:00Z"}}
	if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
		t.Fatal(errRegister)
	}
	if _, errRefresh := manager.ForceRefreshAuth(context.Background(), auth.ID); errRefresh != nil {
		t.Fatalf("valid explicit-path file store cannot refresh: %v, upstream calls=%d", errRefresh, calls)
	}
	if calls != 1 {
		t.Fatalf("upstream calls=%d, want 1", calls)
	}
}

func TestCodexRefreshWithPooledFileStore(t *testing.T) {
	active, vault := t.TempDir(), t.TempDir()
	const name = "codex-pool-refresh.json"
	activePath, vaultPath := filepath.Join(active, name), filepath.Join(vault, name)
	store := sdkauth.NewPooledFileTokenStore(active, vault)
	oldStore, oldTransport := sdkauth.GetTokenStore(), http.DefaultTransport
	t.Cleanup(func() {
		sdkauth.RegisterTokenStore(oldStore)
		http.DefaultTransport = oldTransport
	})
	sdkauth.RegisterTokenStore(store)
	auth := &cliproxyauth.Auth{
		ID: name, Provider: "codex", Attributes: map[string]string{cliproxyauth.AttributePath: activePath},
		Metadata: map[string]any{"type": "codex", "access_token": "old", "refresh_token": "old", "expired": "2000-01-01T00:00:00Z"},
	}
	if _, err := store.Save(cliproxyauth.WithAuthCreationIntent(context.Background()), auth); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"access_token":"new-access","refresh_token":"new-refresh","expires_in":604800}`)), Request: req}, nil
	})
	executor := NewCodexExecutor(&config.Config{AuthDir: active, RuntimeAuthDir: active})
	errs := make(chan error, 2)
	for i := 0; i < cap(errs); i++ {
		go func(snapshot *cliproxyauth.Auth) {
			_, err := executor.Refresh(context.Background(), snapshot)
			errs <- err
		}(auth.Clone())
	}
	for i := 0; i < cap(errs); i++ {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
	if calls.Load() != 1 {
		t.Errorf("OAuth calls=%d, want 1", calls.Load())
	}
	for _, path := range []string{vaultPath, activePath} {
		raw, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(raw), "new-refresh") {
			t.Errorf("refresh missing at %s: %v", path, err)
		}
	}
}
