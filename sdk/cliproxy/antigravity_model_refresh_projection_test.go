package cliproxy

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

func TestAntigravityModelRefreshProjection(t *testing.T) {
	resetAntigravityCapabilityCache()
	t.Cleanup(resetAntigravityCapabilityCache)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	antigravityNowFunc = func() time.Time { return now }
	t.Cleanup(func() { antigravityNowFunc = time.Now })
	manager := coreauth.NewManager(nil, nil, nil)
	svc := &Service{cfg: &config.Config{}, coreManager: manager}
	for i, auth := range []*coreauth.Auth{
		{ID: "route-override", Provider: "Antigravity", Prefix: "team", ProxyURL: "http://proxy.invalid",
			Attributes: map[string]string{"base_urls": " , http://first.invalid/, http://second.invalid/", "excluded_models": "excluded"},
			Metadata:   map[string]any{"access_token": " test-token ", "project_id": "project"}},
		{ID: "metadata-route", Provider: "antigravity", Metadata: map[string]any{"access_token": "test-token", "base_url": "http://metadata.invalid/"}},
		{ID: "default-route", Provider: "antigravity"},
		{ID: "other", Provider: "codex"},
		{ID: "disabled", Provider: "antigravity", Disabled: true},
	} {
		if _, err := manager.Register(t.Context(), auth); err != nil {
			t.Fatal(err)
		}
		auth, _ = manager.GetByID(auth.ID)
		key := svc.antigravityCapabilityKey(auth)
		token, _ := auth.Metadata["access_token"].(string)
		failureKey := key + fmt.Sprintf("/%x", sha256.Sum256([]byte(strings.TrimSpace(token))))
		antigravityCapabilityMu.Lock()
		antigravityCapabilityCache[key] = antigravityCapabilityCacheEntry{expiresAt: now.Add(time.Duration(30-i*5) * time.Second)}
		antigravityAuthFailureCache[failureKey] = antigravityFailureState{nextRetry: now.Add(time.Duration(20-i*5) * time.Second)}
		antigravityCapabilityMu.Unlock()
	}
	for _, candidate := range manager.ListModelRefreshCandidates("antigravity") {
		full, _ := manager.GetByID(candidate.ID)
		if !antigravityAuthModelSettingsEqual(candidate, full) || svc.antigravityCapabilityKey(candidate) != svc.antigravityCapabilityKey(full) {
			t.Fatal("projection changed publication fencing or route key")
		}
	}
	// The other provider and disabled auth have earlier deadlines; neither counts.
	if got := svc.nextAntigravityModelRefreshDelay(); got != 10*time.Second {
		t.Fatalf("retry delay = %s, want 10s", got)
	}
	// Expired deadlines still use the scan bound; expiry still beats a later retry.
	antigravityCapabilityMu.Lock()
	for key, entry := range antigravityCapabilityCache {
		entry.expiresAt = now.Add(-time.Second)
		antigravityCapabilityCache[key] = entry
	}
	for key := range antigravityAuthFailureCache {
		antigravityAuthFailureCache[key] = antigravityFailureState{nextRetry: now.Add(-time.Second)}
	}
	antigravityCapabilityMu.Unlock()
	if got := svc.nextAntigravityModelRefreshDelay(); got != antigravityRefreshScanInterval {
		t.Fatalf("expired delay = %s", got)
	}
	auth, _ := manager.GetByID("route-override")
	key := svc.antigravityCapabilityKey(auth)
	antigravityCapabilityMu.Lock()
	antigravityCapabilityCache[key] = antigravityCapabilityCacheEntry{expiresAt: now.Add(3 * time.Second)}
	antigravityCapabilityMu.Unlock()
	if got := svc.nextAntigravityModelRefreshDelay(); got != 3*time.Second {
		t.Fatalf("expiry delay = %s, want 3s", got)
	}
}
