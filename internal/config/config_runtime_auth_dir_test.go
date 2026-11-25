package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveConfigPreservesAuthoritativeAuthDirWithRuntimePool(t *testing.T) {
	for _, content := range []string{"auth-dir: /vault\n", "oauth:\n  auth-dir: /vault\n"} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		cfg.AuthDir, cfg.RuntimeAuthDir = "/active", "/active"
		cfg.Debug = true
		if err := SaveConfigPreserveComments(path, cfg); err != nil {
			t.Fatal(err)
		}
		saved, err := LoadConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		if saved.AuthDir != "/vault" || !saved.Debug || cfg.AuthDir != "/active" {
			t.Fatalf("saved AuthDir=%q, Debug=%t, runtime AuthDir=%q", saved.AuthDir, saved.Debug, cfg.AuthDir)
		}
	}
}
