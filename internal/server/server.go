// Package server implements the public side of https-tunnel: a control plane that issues sessions on the base domain, and a reverse proxy that serves each session's traffic on its own subdomain by forwarding it down that client's tunnel.
package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/chriswirz/https-tunnel/internal/config"
	"github.com/chriswirz/https-tunnel/internal/tunnel"
)

// Frontend serves the embedded web application.
// It is supplied by main, which owns the go:embed of the Next.js export, and is asked for specific pages by name when the proxy needs to answer with one.
type Frontend interface {
	http.Handler
	// ServePage writes one exported page, such as "404" or "offline", with the given status.
	// It reports false when that page is not in the export, which happens when the binary was built without running the frontend build.
	ServePage(w http.ResponseWriter, r *http.Request, name string, status int) bool
}

// Server is the proxy process.
type Server struct {
	cfg      *config.ServerConfig
	logger   *slog.Logger
	sessions *Manager
	ui       Frontend
	started  time.Time
	shutdown chan struct{}

	readTimeout     time.Duration
	responseTimeout time.Duration
	pingInterval    time.Duration

	// admin holds the signed in browsers; adminMu guards the password hash in cfg, which the UI can change at runtime.
	admin   *adminAuth
	adminMu sync.RWMutex
	// saveAdminHash persists a new password hash, normally into config.json.
	saveAdminHash func(string) error
}

// New builds a server from the server section of the configuration.
// ui is the embedded frontend; passing nil serves the API alone.
// saveAdminHash persists a password chosen in the UI, and may be nil in a build that has nowhere to write it.
func New(cfg *config.ServerConfig, logger *slog.Logger, ui Frontend, saveAdminHash func(string) error) (*Server, error) {
	mgr, err := NewManager(cfg.StateFile, time.Duration(cfg.SessionTTLHours)*time.Hour, cfg.BaseDomain, cfg.PublicScheme)
	if err != nil {
		return nil, fmt.Errorf("loading session state: %w", err)
	}
	if ui == nil {
		ui = apiOnlyFrontend{}
	}
	s := &Server{
		cfg:      cfg,
		logger:   logger,
		sessions: mgr,
		ui:       ui,
		started:  time.Now(),
		shutdown: make(chan struct{}),
		// The read deadline is generous because an idle tunnel only carries pings.
		readTimeout:     3 * time.Minute,
		responseTimeout: 60 * time.Second,
		pingInterval:    30 * time.Second,
		admin:           newAdminAuth(time.Duration(cfg.Admin.SessionHours) * time.Hour),
		saveAdminHash:   saveAdminHash,
	}
	if cfg.Admin.PasswordHash == "" {
		logger.Warn("no admin password set, the web ui accepts admin/admin once and will demand a new password",
			"config", "server.admin.password_hash")
	}
	return s, nil
}

// apiOnlyFrontend stands in when no frontend was embedded, so the API still runs.
type apiOnlyFrontend struct{}

func (apiOnlyFrontend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "404 not found: this build has no web frontend", http.StatusNotFound)
}

func (apiOnlyFrontend) ServePage(http.ResponseWriter, *http.Request, string, int) bool { return false }

// Run serves until the context is cancelled.
func (s *Server) Run(ctx context.Context) error {
	addr := net.JoinHostPort(s.cfg.Addr, fmt.Sprint(s.cfg.Port))
	srv := &http.Server{
		Addr:    addr,
		Handler: s.handler(),
		// Read and write timeouts stay off: tunnels and streamed responses are both long lived.
		ReadHeaderTimeout: 20 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(s.logger.Handler(), slog.LevelDebug),
	}

	go s.reaper(ctx)

	errc := make(chan error, 1)
	go func() {
		if s.cfg.TLS.Enabled() {
			srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
			s.logger.Info("serving https", "addr", addr, "base_domain", s.cfg.BaseDomain, "cert", s.cfg.TLS.CertFile)
			errc <- srv.ListenAndServeTLS(s.cfg.TLS.CertFile, s.cfg.TLS.KeyFile)
			return
		}
		s.logger.Info("serving http", "addr", addr, "base_domain", s.cfg.BaseDomain, "note", "expects a TLS terminating proxy in front")
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		close(s.shutdown)
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
		return nil
	}
}

func (s *Server) reaper(ctx context.Context) {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n := s.sessions.ReapExpired(); n > 0 {
				s.logger.Info("reaped idle sessions", "count", n)
			}
			s.sessions.Touch()
		}
	}
}

// handler routes by Host: the base domain is the control plane and web UI, anything of the form <label>.<base domain> is a tunnel.
func (s *Server) handler() http.Handler {
	control := s.controlPlane()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := hostname(r.Host)
		switch {
		case host == s.cfg.BaseDomain || host == "" || isLoopbackHost(host):
			control.ServeHTTP(w, r)
		case strings.HasSuffix(host, "."+s.cfg.BaseDomain):
			label := strings.TrimSuffix(host, "."+s.cfg.BaseDomain)
			sess := s.sessions.BySubdomain(label)
			if sess == nil {
				s.renderNotFound(w, r)
				return
			}
			s.serveTunnel(w, r, sess)
		default:
			s.renderNotFound(w, r)
		}
	})
}

func (s *Server) controlPlane() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/connect", s.handleConnect)
	mux.HandleFunc("GET /api/v1/tunnel", s.handleTunnel)
	mux.HandleFunc("GET /api/v1/sessions", s.handleListSessions)
	mux.HandleFunc("GET /api/v1/sessions/{id}", s.handleGetSession)
	mux.HandleFunc("DELETE /api/v1/sessions/{id}", s.handleDeleteSession)
	mux.HandleFunc("GET /api/v1/auth/session", s.handleAuthSession)
	mux.HandleFunc("POST /api/v1/auth/login", s.handleAuthLogin)
	mux.HandleFunc("POST /api/v1/auth/logout", s.handleAuthLogout)
	mux.HandleFunc("POST /api/v1/auth/password", s.handleAuthPassword)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, "ok")
	})
	// The spec is unauthenticated on purpose: it documents how to authenticate.
	mux.HandleFunc("GET /openapi.json", s.handleOpenAPI)
	mux.HandleFunc("GET /api/v1/openapi.json", s.handleOpenAPI)
	mux.Handle("/", s.ui)
	return mux
}

// connectRequest is the body of POST /api/v1/connect.
type connectRequest struct {
	SessionID string `json:"session_id,omitempty"`
	// SubdomainRequest is the label the client would like, honoured when it is free.
	SubdomainRequest string `json:"subdomain_request,omitempty"`
	ClientInfo       string `json:"client_info,omitempty"`
}

// connectResponse is what the client gets back, and is the contract quoted in the docs.
type connectResponse struct {
	Session string `json:"session"`
	URL     string `json:"url"`
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	key, ok := s.authenticate(r)
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "invalid api key")
		return
	}
	var req connectRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
			// A missing or malformed body is not fatal: treat it as a request for a fresh session.
			s.logger.Debug("connect body ignored", "err", err)
		}
	}

	if req.SessionID != "" {
		if sess := s.sessions.Get(req.SessionID); sess != nil && sess.KeyName == key.Name {
			url := s.publicURL(r, sess.Subdomain)
			s.logger.Info("session resumed", "session", sess.ID, "url", url, "key", key.Name)
			writeJSON(w, http.StatusOK, connectResponse{Session: sess.ID, URL: url})
			return
		}
		s.logger.Info("unknown session id, issuing a new one", "requested", req.SessionID, "key", key.Name)
	}

	// A key with a pinned subdomain always asks for that one; otherwise the client's own request is used, sanitised to a legal DNS label.
	requested := key.Subdomain
	if requested == "" {
		requested = sanitizeLabel(req.SubdomainRequest)
	}
	sess, honoured, err := s.sessions.Create(key.Name, requested)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if requested != "" && !honoured {
		s.logger.Info("requested subdomain is unavailable, issuing a random one",
			"requested", requested, "issued", sess.Subdomain, "key", key.Name)
	}
	url := s.publicURL(r, sess.Subdomain)
	s.logger.Info("session issued", "session", sess.ID, "url", url, "key", key.Name)
	writeJSON(w, http.StatusOK, connectResponse{Session: sess.ID, URL: url})
}

// handleTunnel upgrades the connection and hands it to the session's read loop.
func (s *Server) handleTunnel(w http.ResponseWriter, r *http.Request) {
	key, ok := s.authenticate(r)
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "invalid api key")
		return
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), tunnel.UpgradeProtocol) {
		writeJSONError(w, http.StatusUpgradeRequired, "expected Upgrade: "+tunnel.UpgradeProtocol)
		return
	}
	sess := s.sessions.Get(r.Header.Get("X-Tunnel-Session"))
	if sess == nil || sess.KeyName != key.Name {
		writeJSONError(w, http.StatusNotFound, "unknown session, call /api/v1/connect first")
		return
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "connection cannot be hijacked")
		return
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "hijack failed")
		return
	}
	_ = conn.SetDeadline(time.Time{})
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetKeepAlive(true)
		_ = tcp.SetKeepAlivePeriod(30 * time.Second)
	}

	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: " + tunnel.UpgradeProtocol + "\r\n" +
		"Connection: Upgrade\r\n\r\n"
	if _, err := brw.WriteString(resp); err != nil {
		conn.Close()
		return
	}
	if err := brw.Flush(); err != nil {
		conn.Close()
		return
	}

	tc := tunnel.NewConn(conn, bufio.NewReaderSize(brw, 32*1024))
	done := sess.attach(tc, clientIP(r, s.cfg.TrustForwardedHeaders))
	if err := tc.WriteJSON(tunnel.FrameHello, tunnel.ControlStream, tunnel.Hello{
		Version:   tunnel.ProtocolVersion,
		Session:   sess.ID,
		URL:       s.publicURL(r, sess.Subdomain),
		ServerNow: time.Now().Unix(),
	}); err != nil {
		sess.detach(tc)
		return
	}
	s.logger.Info("tunnel attached", "session", sess.ID, "url", s.publicURL(r, sess.Subdomain), "peer", conn.RemoteAddr().String())

	go s.keepalive(sess, tc, done)
	go s.readLoop(sess, tc)
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeJSONError(w, http.StatusUnauthorized, "sign in or present an api key")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": s.withRequestScheme(r, s.sessions.List())})
}

// handleOpenAPI serves the embedded specification for this API.
func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	http.ServeContent(w, r, "openapi.json", s.started, bytes.NewReader(openapiJSON))
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeJSONError(w, http.StatusUnauthorized, "sign in or present an api key")
		return
	}
	sess := s.sessions.Get(r.PathValue("id"))
	if sess == nil {
		writeJSONError(w, http.StatusNotFound, "no such session")
		return
	}
	writeJSON(w, http.StatusOK, s.withRequestScheme(r, []SessionView{sess.Snapshot()})[0])
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeJSONError(w, http.StatusUnauthorized, "sign in or present an api key")
		return
	}
	if !s.sessions.Delete(r.PathValue("id")) {
		writeJSONError(w, http.StatusNotFound, "no such session")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// renderOffline answers a request for a session whose client is not attached.
func (s *Server) renderOffline(w http.ResponseWriter, r *http.Request, sess *Session) {
	// A caller expecting JSON, and anything under /api/, gets the error as data.
	// The tunneled application owns those paths, so this is the one case where the proxy answers in its place.
	if strings.Contains(r.Header.Get("Accept"), "application/json") || strings.HasPrefix(r.URL.Path, "/api/") {
		writeJSONError(w, http.StatusBadGateway, "tunnel offline: no client is connected for this session")
		return
	}
	if s.ui.ServePage(w, r, "offline", http.StatusBadGateway) {
		return
	}
	http.Error(w, "502 bad gateway: no client is connected for "+sess.Snapshot().Subdomain, http.StatusBadGateway)
}

// renderNotFound answers a request for a hostname or path that does not exist.
func (s *Server) renderNotFound(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.Header.Get("Accept"), "application/json") || strings.HasPrefix(r.URL.Path, "/api/") {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	if s.ui.ServePage(w, r, "404", http.StatusNotFound) {
		return
	}
	http.NotFound(w, r)
}

// authorized reports whether a request may read or manage sessions.
// Two callers are expected: a tunnel client with an API key, and the web UI with an admin cookie.
// A first boot session that has not yet chosen a password is refused, so nothing is visible until the default password is replaced.
func (s *Server) authorized(r *http.Request) bool {
	if _, ok := s.authenticate(r); ok {
		return true
	}
	sess, ok := s.authSession(r)
	return ok && !sess.mustChangePassword
}

// authenticate checks the bearer token or X-API-Key header against the configured keys.
func (s *Server) authenticate(r *http.Request) (config.APIKey, bool) {
	presented := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if presented == "" {
		presented = strings.TrimSpace(r.Header.Get("X-API-Key"))
	}
	if presented == "" {
		return config.APIKey{}, false
	}
	for _, k := range s.cfg.APIKeys {
		if subtle.ConstantTimeCompare([]byte(k.Key), []byte(presented)) == 1 {
			if k.Name == "" {
				k.Name = "default"
			}
			return k, true
		}
	}
	return config.APIKey{}, false
}

// publicURL builds the address a session is reachable on, using the scheme this very request arrived over.
// A server behind nginx listens on plain HTTP while the world reaches it over TLS, so the config's public_scheme is only a fallback for when the request cannot say.
func (s *Server) publicURL(r *http.Request, subdomain string) string {
	return fmt.Sprintf("%s://%s.%s", s.scheme(r), subdomain, s.cfg.BaseDomain)
}

// withRequestScheme rewrites session URLs so they match how the caller reached this server.
func (s *Server) withRequestScheme(r *http.Request, views []SessionView) []SessionView {
	for i := range views {
		views[i].URL = s.publicURL(r, views[i].Subdomain)
	}
	return views
}

// scheme reports how the caller reached this server.
// Behind a trusted proxy that is whatever it forwarded; on a direct TLS listener it is https; otherwise the configured public_scheme has the last word.
func (s *Server) scheme(r *http.Request) string {
	if s.cfg.TrustForwardedHeaders {
		if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
			return p
		}
	}
	if r.TLS != nil {
		return "https"
	}
	return s.cfg.PublicScheme
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func hostname(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if host, _, err := net.SplitHostPort(h); err == nil {
		return host
	}
	return h
}

func isLoopbackHost(h string) bool {
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// sanitizeLabel keeps a client requested subdomain to a safe DNS label.
func sanitizeLabel(in string) string {
	in = strings.ToLower(strings.TrimSpace(in))
	if len(in) > 40 {
		in = in[:40]
	}
	out := make([]rune, 0, len(in))
	for _, r := range in {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, r)
		case r == '-' && len(out) > 0:
			out = append(out, r)
		}
	}
	return strings.Trim(string(out), "-")
}
