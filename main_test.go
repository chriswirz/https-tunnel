package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chriswirz/https-tunnel/internal/config"
)

// The example is what --example-config prints and what a new install starts
// from, so it has to be a configuration the loader actually accepts.
func TestExampleConfigLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, exampleConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("the shipped example does not load: %v", err)
	}
	if cfg.Client == nil || cfg.Server == nil {
		t.Fatal("the example should show both sections")
	}
	if cfg.Server.Admin.PasswordHash != "" {
		t.Error("the example must not ship a password hash")
	}
	if cfg.Server.BaseDomain == "" {
		t.Error("the example server section needs a base domain")
	}
}

// A checkout that has never run the frontend build still has to compile and
// run, which is what the .gitkeep in web/out is for.
func TestEmbeddedFrontendResolves(t *testing.T) {
	if _, err := webDist.ReadDir("web/out"); err != nil {
		t.Fatalf("web/out is not embedded: %v", err)
	}
}
