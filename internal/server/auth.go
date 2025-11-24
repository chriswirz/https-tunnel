package server

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The web UI signs in as an administrator rather than pasting an API key.
// On a server whose config carries no password hash the first sign in is admin/admin, and the UI then insists on a new password before it will show anything.
// The chosen password is stored in config.json as a PBKDF2-HMAC-SHA256 hash, so the file never holds the password itself.
const (
	defaultAdminUsername = "admin"
	// initialAdminPassword is accepted only while no hash is configured.
	initialAdminPassword = "admin"
	adminCookieName      = "tunnel_admin"
	minUsernameChars     = 3
	maxUsernameChars     = 32
	// pbkdf2Iterations is deliberately slow for an interactive login.
	pbkdf2Iterations = 600000
	pbkdf2KeyLength  = 32
	saltLength       = 16
	minPasswordChars = 8
)

// errBadCredentials is returned for both a wrong username and a wrong password, so a caller cannot tell which was wrong.
var errBadCredentials = errors.New("invalid username or password")

// adminSession is one signed in browser.
type adminSession struct {
	username string
	expires  time.Time
	// mustChangePassword keeps a first-boot session penned in until the password is replaced.
	mustChangePassword bool
}

// adminAuth holds the signed in browsers.
// Sessions live in memory only, so a restart signs everyone out; the password itself lives in config.json as a hash.
type adminAuth struct {
	mu       sync.Mutex
	sessions map[string]adminSession
	ttl      time.Duration
}

func newAdminAuth(ttl time.Duration) *adminAuth {
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	return &adminAuth{sessions: map[string]adminSession{}, ttl: ttl}
}

func (a *adminAuth) issue(username string, mustChange bool) (token string, expires time.Time) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	expires = time.Now().Add(a.ttl)

	a.mu.Lock()
	defer a.mu.Unlock()
	a.reapLocked()
	a.sessions[token] = adminSession{username: username, expires: expires, mustChangePassword: mustChange}
	return token, expires
}

func (a *adminAuth) lookup(token string) (adminSession, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[token]
	if !ok || time.Now().After(s.expires) {
		delete(a.sessions, token)
		return adminSession{}, false
	}
	return s, true
}

func (a *adminAuth) revoke(token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, token)
}

// rename records the account's new name on the session being kept and drops every other one, which were all admitted under the old credentials.
// It also lifts the first boot restriction, since reaching here means the account now has a password of its own.
func (a *adminAuth) rename(keep, username string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for token, s := range a.sessions {
		if token != keep {
			delete(a.sessions, token)
			continue
		}
		s.username = username
		s.mustChangePassword = false
		a.sessions[token] = s
	}
}

func (a *adminAuth) reapLocked() {
	now := time.Now()
	for token, s := range a.sessions {
		if now.After(s.expires) {
			delete(a.sessions, token)
		}
	}
}

// hashPassword returns a self describing PBKDF2 string: pbkdf2-sha256$iterations$salt$key, all base64.
func hashPassword(password string) (string, error) {
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, pbkdf2KeyLength)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s",
		pbkdf2Iterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// verifyPassword checks a password against a stored hash in constant time.
func verifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations <= 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// adminUsername is the configured administrator name, defaulting to "admin" only while none has been chosen.
// There is one account, and renaming it retires the old name: once this returns "crwirz", nothing signs in as "admin" any more, including the first boot path below, which checks the name before it considers the default password.
func (s *Server) adminUsername() string {
	s.adminMu.RLock()
	defer s.adminMu.RUnlock()
	if s.cfg.Admin.Username != "" {
		return s.cfg.Admin.Username
	}
	return defaultAdminUsername
}

// normalizeUsername is what both storing and comparing use, so case and stray whitespace never decide a login.
func normalizeUsername(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// validateUsername keeps the name to something that reads back unambiguously in a log line and a config file.
func validateUsername(name string) error {
	switch {
	case len(name) < minUsernameChars:
		return fmt.Errorf("the username must be at least %d characters", minUsernameChars)
	case len(name) > maxUsernameChars:
		return fmt.Errorf("the username must be at most %d characters", maxUsernameChars)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
		default:
			return errors.New("the username may use letters, digits, dot, dash and underscore only")
		}
	}
	return nil
}

// passwordUnset reports whether this server is still on its first boot, with no administrator password chosen.
func (s *Server) passwordUnset() bool {
	s.adminMu.RLock()
	defer s.adminMu.RUnlock()
	return s.cfg.Admin.PasswordHash == ""
}

// checkAdminPassword validates credentials and reports whether the default password was what let them in.
func (s *Server) checkAdminPassword(username, password string) (mustChange bool, err error) {
	if subtle.ConstantTimeCompare([]byte(normalizeUsername(username)), []byte(normalizeUsername(s.adminUsername()))) != 1 {
		// Still spend the time hashing, so a wrong username is not measurably faster than a wrong password.
		_, _ = hashPassword(password)
		return false, errBadCredentials
	}
	s.adminMu.RLock()
	hash := s.cfg.Admin.PasswordHash
	s.adminMu.RUnlock()

	if hash == "" {
		if subtle.ConstantTimeCompare([]byte(password), []byte(initialAdminPassword)) != 1 {
			return false, errBadCredentials
		}
		return true, nil
	}
	if !verifyPassword(hash, password) {
		return false, errBadCredentials
	}
	return false, nil
}

// authSession returns the admin session for a request's cookie.
func (s *Server) authSession(r *http.Request) (adminSession, bool) {
	c, err := r.Cookie(adminCookieName)
	if err != nil || c.Value == "" {
		return adminSession{}, false
	}
	return s.admin.lookup(c.Value)
}

func (s *Server) setAdminCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     adminCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   s.scheme(r) == "https",
		SameSite: http.SameSiteLaxMode,
	})
}

// authStatus is the body of GET /api/v1/auth/session.
type authStatus struct {
	Authenticated      bool   `json:"authenticated"`
	Username           string `json:"username,omitempty"`
	MustChangePassword bool   `json:"must_change_password"`
	// PasswordUnset tells the login form to prompt with the first boot credentials.
	PasswordUnset bool `json:"password_unset"`
}

func (s *Server) handleAuthSession(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.authSession(r)
	writeJSON(w, http.StatusOK, authStatus{
		Authenticated:      ok,
		Username:           sess.username,
		MustChangePassword: ok && sess.mustChangePassword,
		PasswordUnset:      s.passwordUnset(),
	})
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	mustChange, err := s.checkAdminPassword(req.Username, req.Password)
	if err != nil {
		s.logger.Warn("admin login rejected", "username", req.Username, "peer", clientIP(r, s.cfg.TrustForwardedHeaders))
		writeJSONError(w, http.StatusUnauthorized, errBadCredentials.Error())
		return
	}
	token, expires := s.admin.issue(s.adminUsername(), mustChange)
	s.setAdminCookie(w, r, token, expires)
	s.logger.Info("admin signed in", "username", s.adminUsername(), "must_change_password", mustChange)
	writeJSON(w, http.StatusOK, authStatus{
		Authenticated:      true,
		Username:           s.adminUsername(),
		MustChangePassword: mustChange,
		PasswordUnset:      s.passwordUnset(),
	})
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(adminCookieName); err == nil {
		s.admin.revoke(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: adminCookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	w.WriteHeader(http.StatusNoContent)
}

// handleAuthAccount changes the administrator username, password, or both, and writes the result to config.json.
//
// There is exactly one administrator account. Changing the username renames that account rather than adding another, so the previous name stops working the moment this returns: it is not left behind as a second way in, and the first boot admin/admin path checks the configured name too, so it cannot resurrect the old one either.
func (s *Server) handleAuthAccount(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.authSession(r)
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewUsername     string `json:"new_username,omitempty"`
		NewPassword     string `json:"new_password,omitempty"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if _, err := s.checkAdminPassword(sess.username, req.CurrentPassword); err != nil {
		writeJSONError(w, http.StatusUnauthorized, "the current password is wrong")
		return
	}

	username := normalizeUsername(req.NewUsername)
	renaming := username != "" && username != normalizeUsername(s.adminUsername())
	if username != "" {
		if err := validateUsername(username); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	// A server that has never had a password set must leave with one, whatever else this request changes.
	if s.passwordUnset() && req.NewPassword == "" {
		writeJSONError(w, http.StatusBadRequest, "choose a password: this server is still on the default one")
		return
	}
	if !renaming && req.NewPassword == "" {
		writeJSONError(w, http.StatusBadRequest, "nothing to change: send a new username, a new password, or both")
		return
	}

	var hash string
	if req.NewPassword != "" {
		switch {
		case len(req.NewPassword) < minPasswordChars:
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("the new password must be at least %d characters", minPasswordChars))
			return
		case req.NewPassword == initialAdminPassword:
			writeJSONError(w, http.StatusBadRequest, "choose something other than the default password")
			return
		}
		var err error
		if hash, err = hashPassword(req.NewPassword); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "could not hash the password")
			return
		}
	}

	if s.saveAdmin == nil {
		writeJSONError(w, http.StatusInternalServerError, "this server cannot persist an account change")
		return
	}
	if err := s.saveAdmin(username, hash); err != nil {
		s.logger.Error("could not write the account change to the config file", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "could not save the change: "+err.Error())
		return
	}

	previous := s.adminUsername()
	s.adminMu.Lock()
	if username != "" {
		s.cfg.Admin.Username = username
	}
	if hash != "" {
		s.cfg.Admin.PasswordHash = hash
	}
	current := s.cfg.Admin.Username
	s.adminMu.Unlock()

	// Every other session was admitted under the old credentials.
	token := ""
	if c, err := r.Cookie(adminCookieName); err == nil {
		token = c.Value
	}
	s.admin.rename(token, current)

	switch {
	case renaming && hash != "":
		s.logger.Info("admin account renamed and password changed", "from", previous, "to", current)
	case renaming:
		s.logger.Info("admin account renamed", "from", previous, "to", current)
	default:
		s.logger.Info("admin password changed", "username", current)
	}
	writeJSON(w, http.StatusOK, authStatus{Authenticated: true, Username: current})
}
