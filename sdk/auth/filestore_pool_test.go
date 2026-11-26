package auth

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/auth/codex"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func TestPooledFileStoreMutatesVaultAndEvictionIsNotDeletion(t *testing.T) {
	ctx := context.Background()
	vault, active := t.TempDir(), t.TempDir()
	store := NewPooledFileTokenStore(active, vault)
	store.SetBaseDir(vault)
	const name = "codex-test.json"
	vaultPath, activePath := filepath.Join(vault, name), filepath.Join(active, name)
	before := map[string]any{"type": "codex", "access_token": "first", "refresh_token": "initial"}
	if err := store.WriteManagedAuthFile(activePath, true, func(path string) error {
		data, _ := json.Marshal(before)
		return os.WriteFile(path, data, 0600)
	}); err != nil {
		t.Fatal(err)
	}
	// Leave an unselected credential in the same authoritative vault.
	if err := os.WriteFile(filepath.Join(vault, "unselected.json"), []byte(`{"type":"codex"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if auths, err := store.List(ctx); err != nil || len(auths) != 1 {
		t.Fatalf("pool list count = %d, err = %v", len(auths), err)
	}
	after := map[string]any{"type": "codex", "access_token": "second", "refresh_token": "updated"}
	refresh := &cliproxyauth.Auth{
		ID: name, Provider: "codex", Metadata: after,
		CodexRefreshPatch: codex.NewCredentialRefreshPatch(before, after, true, false),
	}
	if saved, err := store.Save(ctx, refresh); err != nil || saved != activePath {
		t.Fatalf("refresh path = %q, err = %v", saved, err)
	}
	for _, path := range []string{activePath, vaultPath} {
		data, err := os.ReadFile(path)
		var payload map[string]any
		if err != nil || json.Unmarshal(data, &payload) != nil || payload["refresh_token"] != "updated" {
			t.Fatalf("refresh was not published to %q: %v", path, err)
		}
	}
	if err := os.Remove(activePath); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(ctx, refresh); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(activePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("evicted refresh re-admitted the account: %v", err)
	}
	if _, err := os.Stat(vaultPath); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, activePath); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(ctx, refresh); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(vaultPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("late refresh resurrected the deleted account: %v", err)
	}
}
