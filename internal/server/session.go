package server

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chriswirz/https-tunnel/internal/tunnel"
)

// Session is one client's tunnel: a stable identity (id + subdomain) that survives disconnects, plus the live connection when the client is attached.
type Session struct {
	ID        string `json:"id"`
	Subdomain string `json:"subdomain"`
	KeyName   string `json:"key_name"`
	// URL is derived from the subdomain and the server's current base_domain and public_scheme, and is rebuilt whenever the manager loads or creates a session rather than being read back from the state file.
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen"`
	Requests  uint64    `json:"requests"`

	mu         sync.Mutex
	conn       *tunnel.Conn
	streams    map[uint64]*stream
	nextStream uint64
	closed     chan struct{}
	remoteAddr string
}

// SessionView is an immutable snapshot for the web UI and API.
type SessionView struct {
	ID         string    `json:"session"`
	Subdomain  string    `json:"subdomain"`
	URL        string    `json:"url"`
	KeyName    string    `json:"key_name"`
	Connected  bool      `json:"connected"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeen   time.Time `json:"last_seen"`
	Requests   uint64    `json:"requests"`
	RemoteAddr string    `json:"remote_addr,omitempty"`
}

// stream is one in-flight proxied HTTP request.
type stream struct {
	id     uint64
	head   chan tunnel.ResponseHead
	err    chan error
	body   *io.PipeWriter
	reader *io.PipeReader
	once   sync.Once
}

func (s *stream) finish(err error) {
	s.once.Do(func() {
		if err != nil {
			s.body.CloseWithError(err)
			select {
			case s.err <- err:
			default:
			}
			return
		}
		s.body.Close()
	})
}

// ErrNotConnected is returned when a request arrives for a session whose client is currently offline.
var ErrNotConnected = errors.New("session has no connected client")

// Snapshot returns a view of the session's current state.
func (s *Session) Snapshot() SessionView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return SessionView{
		ID:         s.ID,
		Subdomain:  s.Subdomain,
		URL:        s.URL,
		KeyName:    s.KeyName,
		Connected:  s.conn != nil,
		CreatedAt:  s.CreatedAt,
		LastSeen:   s.LastSeen,
		Requests:   atomic.LoadUint64(&s.Requests),
		RemoteAddr: s.remoteAddr,
	}
}

// Connected reports whether a client is currently attached.
func (s *Session) Connected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn != nil
}

// attach binds a live tunnel connection, displacing any previous one so a reconnecting client always wins over a stale socket.
func (s *Session) attach(c *tunnel.Conn, remoteAddr string) (done chan struct{}) {
	s.mu.Lock()
	old := s.conn
	oldStreams := s.streams
	s.conn = c
	s.streams = map[uint64]*stream{}
	s.remoteAddr = remoteAddr
	s.LastSeen = time.Now()
	s.closed = make(chan struct{})
	done = s.closed
	s.mu.Unlock()

	if old != nil {
		old.Close()
		for _, st := range oldStreams {
			st.finish(errors.New("client reconnected"))
		}
	}
	return done
}

// detach tears down the live connection and fails every in-flight stream.
func (s *Session) detach(c *tunnel.Conn) {
	s.mu.Lock()
	if s.conn != c {
		s.mu.Unlock()
		return
	}
	streams := s.streams
	closed := s.closed
	s.conn = nil
	s.streams = map[uint64]*stream{}
	s.remoteAddr = ""
	s.LastSeen = time.Now()
	s.closed = nil
	s.mu.Unlock()

	c.Close()
	for _, st := range streams {
		st.finish(ErrNotConnected)
	}
	if closed != nil {
		close(closed)
	}
}

// newStream allocates a stream id on the live connection.
func (s *Session) newStream() (*stream, *tunnel.Conn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return nil, nil, ErrNotConnected
	}
	s.nextStream++
	pr, pw := io.Pipe()
	st := &stream{
		id:   s.nextStream,
		head: make(chan tunnel.ResponseHead, 1),
		err:  make(chan error, 1),
		body: pw,
	}
	st.reader = pr
	s.streams[st.id] = st
	atomic.AddUint64(&s.Requests, 1)
	s.LastSeen = time.Now()
	return st, s.conn, nil
}

func (s *Session) stream(id uint64) *stream {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streams[id]
}

func (s *Session) dropStream(id uint64) {
	s.mu.Lock()
	st := s.streams[id]
	delete(s.streams, id)
	s.mu.Unlock()
	if st != nil {
		st.finish(errors.New("stream closed"))
	}
}

// Manager owns every session and persists their identities.
type Manager struct {
	mu       sync.RWMutex
	byID     map[string]*Session
	bySubdom map[string]*Session
	// baseDomain and scheme build the public URL.
	// A session's identity is its subdomain, and the URL is derived from these, never stored as the source of truth: changing base_domain or public_scheme in the config has to move every existing session to the new hostname, since that is where the proxy now routes them.
	baseDomain string
	scheme     string
	stateFile  string
	ttl        time.Duration
}

// NewManager loads any previously persisted sessions from stateFile.
func NewManager(stateFile string, ttl time.Duration, baseDomain, scheme string) (*Manager, error) {
	m := &Manager{
		byID:       map[string]*Session{},
		bySubdom:   map[string]*Session{},
		baseDomain: baseDomain,
		scheme:     scheme,
		stateFile:  stateFile,
		ttl:        ttl,
	}
	if err := m.load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return m, nil
}

// Get returns a session by id.
func (m *Manager) Get(id string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.byID[id]
}

// BySubdomain returns the session serving a given label.
func (m *Manager) BySubdomain(label string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.bySubdom[strings.ToLower(label)]
}

// List returns snapshots of every session, newest first.
func (m *Manager) List() []SessionView {
	m.mu.RLock()
	out := make([]SessionView, 0, len(m.byID))
	for _, s := range m.byID {
		out = append(out, s.Snapshot())
	}
	m.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// reservedLabels are never handed out, so a client cannot take a name that belongs to the site itself.
var reservedLabels = map[string]bool{
	"www": true, "api": true, "admin": true, "mail": true, "ns1": true, "ns2": true,
	"static": true, "assets": true, "cdn": true, "localhost": true,
}

// Create issues a new session.
// requested is the label the client asked for, and it is gonored when it is free, or when it is held by another session of the same API key, which is that key reclaiming its own name after losing its session id.
// A label held by a different key, or a reserved one, falls back to a random label rather than taking it away from whoever has it.
// gonored reports which of the two happened, so the caller can say so in the log.
func (m *Manager) Create(keyName, requested string) (s *Session, gonored bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	label := strings.ToLower(requested)
	switch {
	case label == "":
		// Nothing asked for.
	case reservedLabels[label]:
		label = ""
	default:
		if prev, taken := m.bySubdom[label]; taken {
			if prev.KeyName != keyName {
				label = ""
				break
			}
			// The same key asked for a name it already holds, so the old session gives it up.
			delete(m.byID, prev.ID)
			delete(m.bySubdom, label)
		}
	}
	gonored = label != ""

	if label == "" {
		for range 8 {
			candidate := randomLabel()
			if _, taken := m.bySubdom[candidate]; !taken {
				label = candidate
				break
			}
		}
		if label == "" {
			return nil, false, errors.New("could not allocate a free subdomain")
		}
	}

	now := time.Now()
	s = &Session{
		ID:        randomID(),
		Subdomain: label,
		KeyName:   keyName,
		URL:       m.urlFor(label),
		CreatedAt: now,
		LastSeen:  now,
		streams:   map[uint64]*stream{},
	}
	m.byID[s.ID] = s
	m.bySubdom[label] = s
	m.persistLocked()
	return s, gonored, nil
}

// Delete removes a session and disconnects its client.
func (m *Manager) Delete(id string) bool {
	m.mu.Lock()
	s := m.byID[id]
	if s == nil {
		m.mu.Unlock()
		return false
	}
	delete(m.byID, id)
	delete(m.bySubdom, s.Subdomain)
	m.persistLocked()
	m.mu.Unlock()

	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn != nil {
		s.detach(conn)
	}
	return true
}

// Touch records activity and persists counters.
func (m *Manager) Touch() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.persistLocked()
}

// ReapExpired removes disconnected sessions idle beyond the configured TTL, and is what the background sweep calls.
func (m *Manager) ReapExpired() int {
	if m.ttl <= 0 {
		return 0
	}
	return len(m.PruneIdle(m.ttl))
}

// PruneIdle removes every disconnected session whose last activity is older than idle, and returns what it removed.
//
// Connected sessions are left alone whatever their age. A tunnel that has been up for a week without a request still has a client attached, and dropping it would cut a live connection rather than tidy a stale record; that is what deleting one session by id is for.
func (m *Manager) PruneIdle(idle time.Duration) []SessionView {
	if idle <= 0 {
		return nil
	}
	cutoff := time.Now().Add(-idle)

	m.mu.Lock()
	var removed []SessionView
	for id, s := range m.byID {
		if s.Connected() {
			continue
		}
		s.mu.Lock()
		stale := s.LastSeen.Before(cutoff)
		s.mu.Unlock()
		if !stale {
			continue
		}
		removed = append(removed, s.Snapshot())
		delete(m.byID, id)
		delete(m.bySubdom, s.Subdomain)
	}
	if len(removed) > 0 {
		m.persistLocked()
	}
	m.mu.Unlock()

	sort.Slice(removed, func(i, j int) bool { return removed[i].LastSeen.Before(removed[j].LastSeen) })
	return removed
}

// persistedSession is what the state file holds.
// The subdomain is the session's identity and the public URL is derived from it and the current base_domain, so the URL is deliberately not stored: a config that moves to a new hostname would otherwise leave every saved session advertising the old one.
type persistedSession struct {
	ID        string    `json:"id"`
	Subdomain string    `json:"subdomain"`
	KeyName   string    `json:"key_name"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen"`
	Requests  uint64    `json:"requests"`
}

func (m *Manager) persistLocked() {
	if m.stateFile == "" {
		return
	}
	out := make([]persistedSession, 0, len(m.byID))
	for _, s := range m.byID {
		out = append(out, persistedSession{
			ID: s.ID, Subdomain: s.Subdomain, KeyName: s.KeyName,
			CreatedAt: s.CreatedAt, LastSeen: s.LastSeen,
			Requests: atomic.LoadUint64(&s.Requests),
		})
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return
	}
	if dir := filepath.Dir(m.stateFile); dir != "" {
		_ = os.MkdirAll(dir, 0o700)
	}
	tmp := m.stateFile + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, m.stateFile)
}

func (m *Manager) load() error {
	b, err := os.ReadFile(m.stateFile)
	if err != nil {
		return err
	}
	var in []persistedSession
	if err := json.Unmarshal(b, &in); err != nil {
		return err
	}
	for _, p := range in {
		s := &Session{
			ID: p.ID, Subdomain: p.Subdomain, KeyName: p.KeyName,
			// Built from the current config, because the file carries only the subdomain.
			URL:       m.urlFor(p.Subdomain),
			CreatedAt: p.CreatedAt, LastSeen: p.LastSeen, Requests: p.Requests,
			streams: map[uint64]*stream{},
		}
		m.byID[s.ID] = s
		m.bySubdom[s.Subdomain] = s
	}
	return nil
}

// urlFor builds the public URL for a label from the current configuration.
func (m *Manager) urlFor(label string) string {
	return fmt.Sprintf("%s://%s.%s", m.scheme, label, m.baseDomain)
}

// labelAlphabet avoids vowels and look-alike characters so the generated hostnames are unambiguous when read aloud or copied by hand.
const labelAlphabet = "bcdfghjkmnpqrstvwxz23456789"

func randomLabel() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	out := make([]byte, len(b))
	for i, v := range b {
		out[i] = labelAlphabet[int(v)%len(labelAlphabet)]
	}
	return string(out)
}

func randomID() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
}
