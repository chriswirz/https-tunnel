// Package config loads and persists the https-tunnel configuration file.
//
// A single configuration file describes both roles the binary can play: the "client" section drives an outbound tunnel to a proxy server, the "server" section drives the public-facing proxy itself.
// Either section may be omitted; whatever is present (and enabled) is what runs.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Config is the on-disk configuration document.
type Config struct {
	Client *ClientConfig `json:"client,omitempty"`
	Server *ServerConfig `json:"server,omitempty"`

	path string
	mu   sync.Mutex
}

// ClientConfig describes the local side of a tunnel.
type ClientConfig struct {
	// Enabled turns the client on.
	// Defaults to true when the section exists.
	Enabled *bool `json:"enabled,omitempty"`
	// APIKey authenticates against the proxy server's control plane.
	APIKey string `json:"api_key"`
	// ServerURL is the control-plane base URL, e.g. https://tunnel.example.com.
	ServerURL string `json:"server_url"`
	// SessionID, when present, asks the server to resume a previous session so the public URL stays stable.
	// It is written back on first connect.
	SessionID string `json:"session_id,omitempty"`
	// LocalPort is the local HTTP port to expose (e.g. 8756 for an MCP server).
	// Leave it out and set LocalDir instead to serve files rather than proxy to a server.
	LocalPort int `json:"local_port,omitempty"`
	// LocalDir serves a directory over the tunnel instead of proxying to a port.
	// At least one of LocalPort and LocalDir is required; when both are set the port takes precedence and the directory is ignored, so a folder can stay configured while a local server is running.
	LocalDir string `json:"local_dir,omitempty"`
	// CacheMB is the size of the in memory LRU that holds frequently served files, in megabytes.
	// It applies to LocalDir only. 0 disables caching and reads every request from the disk.
	CacheMB int `json:"cache_mb,omitempty"`
	// DirectoryListing lists a directory that has no index.html. Off by default, so nothing is exposed by accident.
	DirectoryListing bool `json:"directory_listing,omitempty"`
	// LocalHost defaults to 127.0.0.1.
	LocalHost string `json:"local_host,omitempty"`
	// LocalScheme is http or https.
	// Defaults to http.
	LocalScheme string `json:"local_scheme,omitempty"`
	// InsecureSkipVerify disables TLS verification toward the local target.
	InsecureSkipVerify bool `json:"insecure_skip_verify,omitempty"`
	// SubdomainRequest asks the server for a specific label instead of a random one, so "chris" asks for https://chris.<base domain>/.
	// It is granted when the label is free; when another key's session already holds it, a random label is issued as usual.
	SubdomainRequest string `json:"subdomain_request,omitempty"`
}

// ServerConfig describes the public-facing proxy.
type ServerConfig struct {
	Enabled *bool `json:"enabled,omitempty"`
	// Port to listen on.
	// Typically plain HTTP behind nginx.
	Port int `json:"port"`
	// Addr is the bind address.
	// Defaults to 0.0.0.0.
	Addr string `json:"addr,omitempty"`
	// BaseDomain is the control-plane host.
	// Tunnels are served on <label>.<base_domain>.
	BaseDomain string `json:"base_domain"`
	// PublicScheme is the scheme advertised in issued URLs.
	// Defaults to https.
	PublicScheme string `json:"public_scheme,omitempty"`
	// APIKeys are the keys accepted from clients.
	APIKeys []APIKey `json:"api_keys"`
	// StateFile persists sessions across restarts so clients can reconnect.
	StateFile string `json:"state_file,omitempty"`
	// SessionTTLHours expires idle, disconnected sessions. 0 disables expiry.
	SessionTTLHours int `json:"session_ttl_hours,omitempty"`
	// TLS, when both files are set, makes the server serve HTTPS directly instead of plain HTTP (i.e. no nginx in front).
	TLS TLSConfig `json:"tls,omitempty"`
	// TrustForwardedHeaders gonors X-Forwarded-For/Proto from a front proxy.
	TrustForwardedHeaders bool `json:"trust_forwarded_headers,omitempty"`
	// Admin is the account that signs in to the web UI.
	Admin AdminConfig `json:"admin,omitempty"`
}

// AdminConfig is the web UI's administrator account.
// The password is never stored: password_hash holds a PBKDF2-HMAC-SHA256 digest written by the server itself when the password is set from the UI.
// While it is empty the server is on its first boot and accepts admin/admin once, then requires a new password before anything else can be done.
type AdminConfig struct {
	// Username defaults to "admin".
	Username string `json:"username,omitempty"`
	// PasswordHash is written by the server; there is no need to edit it by hand.
	PasswordHash string `json:"password_hash,omitempty"`
	// SessionHours is how long a browser stays signed in. Defaults to 12.
	SessionHours int `json:"session_hours,omitempty"`
}

// APIKey is a credential a client may present.
type APIKey struct {
	Key  string `json:"key"`
	Name string `json:"name,omitempty"`
	// Subdomain pins every session opened with this key to a fixed label, overriding whatever the client asks for.
	Subdomain string `json:"subdomain,omitempty"`
}

// TLSConfig points at a certificate/key pair for direct HTTPS serving.
type TLSConfig struct {
	CertFile string `json:"cert_file,omitempty"`
	KeyFile  string `json:"key_file,omitempty"`
}

// ServesDirectory reports whether this client serves files rather than proxying to a local server.
// Both may be configured at once, which is convenient for a machine that sometimes runs the server and sometimes only has the files: the port wins, and the directory is the standing fallback.
func (c *ClientConfig) ServesDirectory() bool {
	return c != nil && c.LocalDir != "" && c.LocalPort == 0
}

// IsEnabled reports whether the client section should run.
func (c *ClientConfig) IsEnabled() bool { return c != nil && (c.Enabled == nil || *c.Enabled) }

// IsEnabled reports whether the server section should run.
func (s *ServerConfig) IsEnabled() bool { return s != nil && (s.Enabled == nil || *s.Enabled) }

// Enabled reports whether direct TLS serving is configured.
func (t TLSConfig) Enabled() bool { return t.CertFile != "" && t.KeyFile != "" }

// Load reads and validates the configuration file at path.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{path: path}
	if err := json.Unmarshal([]byte(stripComments(string(raw))), cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Client != nil {
		if c.Client.LocalHost == "" {
			c.Client.LocalHost = "127.0.0.1"
		}
		if c.Client.LocalScheme == "" {
			c.Client.LocalScheme = "http"
		}
		if c.Client.LocalDir != "" {
			if abs, err := filepath.Abs(c.Client.LocalDir); err == nil {
				c.Client.LocalDir = abs
			}
		}
		c.Client.ServerURL = strings.TrimRight(c.Client.ServerURL, "/")
	}
	if c.Server != nil {
		if c.Server.Port == 0 {
			c.Server.Port = 8080
		}
		if c.Server.Addr == "" {
			c.Server.Addr = "0.0.0.0"
		}
		if c.Server.PublicScheme == "" {
			c.Server.PublicScheme = "https"
		}
		if c.Server.StateFile == "" {
			c.Server.StateFile = filepath.Join(filepath.Dir(c.path), "sessions.json")
		}
		c.Server.BaseDomain = strings.ToLower(strings.TrimSpace(c.Server.BaseDomain))
		if c.Server.Admin.Username == "" {
			c.Server.Admin.Username = "admin"
		}
		if c.Server.Admin.SessionHours == 0 {
			c.Server.Admin.SessionHours = 12
		}
	}
}

func (c *Config) validate() error {
	if !c.Client.IsEnabled() && !c.Server.IsEnabled() {
		return errors.New("config: neither the client nor the server section is enabled")
	}
	if c.Client.IsEnabled() {
		switch {
		case c.Client.APIKey == "":
			return errors.New("config: client.api_key is required")
		case c.Client.ServerURL == "":
			return errors.New("config: client.server_url is required")
		case c.Client.LocalPort == 0 && c.Client.LocalDir == "":
			return errors.New("config: client needs local_port (proxy to a local server) or local_dir (serve a folder)")
		case c.Client.LocalPort != 0 && (c.Client.LocalPort < 0 || c.Client.LocalPort > 65535):
			return errors.New("config: client.local_port must be 1-65535")
		case c.Client.CacheMB < 0:
			return errors.New("config: client.cache_mb cannot be negative")
		}
		if c.Client.LocalDir != "" {
			info, err := os.Stat(c.Client.LocalDir)
			switch {
			case err != nil:
				return fmt.Errorf("config: client.local_dir %q: %w", c.Client.LocalDir, err)
			case !info.IsDir():
				return fmt.Errorf("config: client.local_dir %q is not a directory", c.Client.LocalDir)
			}
		}
	}
	if c.Server.IsEnabled() {
		if c.Server.BaseDomain == "" {
			return errors.New("config: server.base_domain is required")
		}
		if len(c.Server.APIKeys) == 0 {
			return errors.New("config: server.api_keys must contain at least one key")
		}
		for i, k := range c.Server.APIKeys {
			if k.Key == "" {
				return fmt.Errorf("config: server.api_keys[%d].key is empty", i)
			}
		}
		if (c.Server.TLS.CertFile == "") != (c.Server.TLS.KeyFile == "") {
			return errors.New("config: server.tls needs both cert_file and key_file")
		}
	}
	return nil
}

// Path returns the file the config was loaded from.
func (c *Config) Path() string { return c.path }

// SaveSessionID writes a newly issued session id back to the config file so a later run resumes the same public URL.
func (c *Config) SaveSessionID(id string) error {
	c.mu.Lock()
	if c.Client == nil || c.Client.SessionID == id {
		c.mu.Unlock()
		return nil
	}
	c.Client.SessionID = id
	c.mu.Unlock()

	return c.updateSection("client", "session_id", func(section map[string]json.RawMessage) error {
		enc, err := json.Marshal(id)
		if err != nil {
			return err
		}
		section["session_id"] = enc
		return nil
	})
}

// SaveAdminAccount writes the administrator username and password hash into the server section of the config file.
// An empty hash leaves the stored one alone, so a rename does not have to know the password.
// The account is singular: renaming it retires the old name completely, which is why the username is written rather than added.
func (c *Config) SaveAdminAccount(username, passwordHash string) error {
	return c.updateSection("server", "admin", func(section map[string]json.RawMessage) error {
		admin := map[string]json.RawMessage{}
		if b, ok := section["admin"]; ok {
			if err := json.Unmarshal(b, &admin); err != nil {
				return err
			}
		}
		if username != "" {
			enc, err := json.Marshal(username)
			if err != nil {
				return err
			}
			admin["username"] = enc
		}
		if passwordHash != "" {
			enc, err := json.Marshal(passwordHash)
			if err != nil {
				return err
			}
			admin["password_hash"] = enc
		}
		updated, err := json.Marshal(admin)
		if err != nil {
			return err
		}
		section["admin"] = updated
		return nil
	})
}

// updateSection rewrites one top level section of the config file under the lock, leaving every other key as it was found.
// field names the key being changed and is used only for the error message.
func (c *Config) updateSection(name, field string, mutate func(map[string]json.RawMessage) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	raw, err := os.ReadFile(c.path)
	if err != nil {
		return err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stripComments(string(raw))), &doc); err != nil {
		return err
	}
	section := map[string]json.RawMessage{}
	if b, ok := doc[name]; ok {
		if err := json.Unmarshal(b, &section); err != nil {
			return err
		}
	}
	if err := mutate(section); err != nil {
		return fmt.Errorf("updating %s.%s: %w", name, field, err)
	}
	if doc[name], err = json.Marshal(section); err != nil {
		return err
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, append(out, 0x0a), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// stripComments removes // line comments so the config file may carry notes.
func stripComments(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inString, escaped := false, false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inString {
			b.WriteByte(ch)
			switch {
			case escaped:
				escaped = false
			case ch == '\\':
				escaped = true
			case ch == '"':
				inString = false
			}
			continue
		}
		if ch == '"' {
			inString = true
			b.WriteByte(ch)
			continue
		}
		if ch == '/' && i+1 < len(s) && s[i+1] == '/' {
			for i < len(s) && s[i] != '\n' {
				i++
			}
			b.WriteByte('\n')
			continue
		}
		b.WriteByte(ch)
	}
	return b.String()
}
