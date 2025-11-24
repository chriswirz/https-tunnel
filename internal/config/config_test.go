package config

import (
	"os"
	"path/filepath"
	"testing"
)

const sample = `{
  // a comment, and a url inside a string must survive: https://example.com/x.
  "client": {
    "api_key": "k",
    "server_url": "https://tunnel.example.com/",
    "local_port": 8756
  },
  "server": {
    "base_domain": "tunnel.example.com",
    "api_keys": [{ "key": "k", "name": "test" }]
  }
}`

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadAppliesDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, sample))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Client.LocalHost != "127.0.0.1" || cfg.Client.LocalScheme != "http" {
		t.Errorf("client defaults not applied: %+v", cfg.Client)
	}
	if cfg.Client.ServerURL != "https://tunnel.example.com" {
		t.Errorf("trailing slash not trimmed: %q", cfg.Client.ServerURL)
	}
	if cfg.Server.Port != 8080 || cfg.Server.PublicScheme != "https" || cfg.Server.Addr != "0.0.0.0" {
		t.Errorf("server defaults not applied: %+v", cfg.Server)
	}
	if !cfg.Client.IsEnabled() || !cfg.Server.IsEnabled() {
		t.Error("sections without an explicit enabled flag should be on")
	}
}

func TestSaveSessionIDRoundTrips(t *testing.T) {
	path := writeConfig(t, sample)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.SaveSessionID("sess-1"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Client.SessionID != "sess-1" {
		t.Errorf("session id not persisted, got %q", reloaded.Client.SessionID)
	}
	if reloaded.Server.BaseDomain != "tunnel.example.com" {
		t.Error("rewriting the config dropped the server section")
	}
}

// Both targets may be configured; the port is what runs, so a folder can stay
// in the file while a local server is up.
func TestLocalPortTakesPrecedenceOverLocalDir(t *testing.T) {
	dir := t.TempDir()
	body := `{"client":{"api_key":"k","server_url":"https://x","local_port":8756,"local_dir":"` +
		filepath.ToSlash(dir) + `"}}`
	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("a config with both should load: %v", err)
	}
	if cfg.Client.ServesDirectory() {
		t.Error("the port should win when both are set")
	}
	if cfg.Client.LocalDir == "" {
		t.Error("the directory should still be readable from the config")
	}

	// With the port removed, the same directory is what gets served.
	onlyDir := `{"client":{"api_key":"k","server_url":"https://x","local_dir":"` + filepath.ToSlash(dir) + `"}}`
	cfg, err = Load(writeConfig(t, onlyDir))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Client.ServesDirectory() {
		t.Error("with no port, the directory should be served")
	}
}

func TestValidateRejectsIncompleteSections(t *testing.T) {
	cases := map[string]string{
		"client without a key":                        `{"client":{"server_url":"https://x","local_port":1}}`,
		"client with neither a port nor a directory":  `{"client":{"api_key":"k","server_url":"https://x"}}`,
		"client with a directory that does not exist": `{"client":{"api_key":"k","server_url":"https://x","local_dir":"/no/such/place/at/all"}}`,
		"server without a key":                        `{"server":{"base_domain":"x","api_keys":[]}}`,
		"half a tls pair":                             `{"server":{"base_domain":"x","api_keys":[{"key":"k"}],"tls":{"cert_file":"c"}}}`,
		"nothing enabled":                             `{}`,
	}
	for name, body := range cases {
		if _, err := Load(writeConfig(t, body)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestStripCommentsLeavesStringsAlone(t *testing.T) {
	in := `{"a":"https://x/y", // trailing
 "b":"c"}`
	want := "{\"a\":\"https://x/y\", \n \"b\":\"c\"}"
	if got := stripComments(in); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
