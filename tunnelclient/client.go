// Package tunnelclient opens an https-tunnel from inside your own program.
//
// It is the same client the https-tunnel binary runs, without the configuration file: an application that already serves HTTP can expose itself on a public HTTPS URL without running a second process alongside it.
//
// The usual shape for an embedded client is to hand over the http.Handler you already have, so requests never touch a local socket at all:
//
//	tc, err := tunnelclient.New(tunnelclient.Options{
//	    APIKey:    os.Getenv("TUNNEL_API_KEY"),
//	    ServerURL: "https://tunnel.example.com",
//	    Handler:   myMCPServer,
//	    OnConnect: func(t tunnelclient.Tunnel) { log.Printf("public url: %s", t.URL) },
//	})
//	if err != nil {
//	    return err
//	}
//	go tc.Run(ctx) // reconnects on its own until ctx is canceled
//
// The alternatives are TargetURL, to proxy to a server already listening locally, and Dir, to serve a folder from disk.
package tunnelclient

import (
	"bufio"
	"cmp"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/chriswirz/https-tunnel/internal/tunnel"
)

// Options configures a Client.
// Exactly one of Handler, TargetURL and Dir says what the tunnel serves.
type Options struct {
	// APIKey authenticates against the tunnel server. Required.
	APIKey string
	// ServerURL is the control plane, for example https://tunnel.example.com. Required.
	ServerURL string

	// SessionID resumes a previous session, keeping its public URL.
	// Leave it empty on a first run and persist whatever OnSession reports.
	SessionID string
	// SubdomainRequest asks for a particular label, so "chris" asks for https://chris.<base domain>/.
	// The server grants it when the label is free and quietly issues a random one otherwise, so read the URL from Tunnel or OnConnect rather than assuming.
	SubdomainRequest string

	// Handler serves tunneled requests in process, with no local socket involved.
	// This is the option to use when embedding the client in the application being exposed.
	Handler http.Handler
	// TargetURL proxies to a server already listening locally, for example http://127.0.0.1:8756.
	TargetURL string
	// Dir serves a directory from disk.
	Dir string
	// CacheBytes sizes the in memory LRU that holds frequently served files. Applies to Dir only; zero reads every request from the disk.
	CacheBytes int64
	// DirectoryListing lists a directory that has no index.html. Applies to Dir only.
	DirectoryListing bool

	// InsecureSkipVerify disables certificate verification toward TargetURL, for a local server with a self signed certificate.
	// It has no effect on the connection to the tunnel server, which is always verified.
	InsecureSkipVerify bool

	// Logger receives progress and errors. Defaults to slog.Default().
	Logger *slog.Logger
	// OnSession is called with a session id the moment the server issues one, so the caller can persist it and reclaim the same URL next time.
	OnSession func(sessionID string) error
	// OnConnect is called each time the tunnel is established, including after a reconnect.
	OnConnect func(Tunnel)
	// ClientInfo is free text recorded in the server's log.
	ClientInfo string

	// ReconnectMin and ReconnectMax bound the backoff between attempts. Zero means one second and thirty seconds.
	ReconnectMin, ReconnectMax time.Duration
}

// Tunnel describes a live tunnel.
type Tunnel struct {
	// URL is the public address this tunnel is served on.
	URL string
	// Session is the id that reclaims this URL on a later run.
	Session string
	// Subdomain is the label the URL was built from.
	Subdomain string
}

// Client is one tunnel. It is safe for concurrent use.
type Client struct {
	opts   Options
	logger *slog.Logger

	target     *url.URL
	httpClient *http.Client
	// local serves requests in process, for the Handler and Dir modes.
	local http.Handler
	files *fileServer

	mu      sync.Mutex
	streams map[uint64]*inbound
	current Tunnel
}

// inbound is one request being replayed against the local target.
type inbound struct {
	body *io.PipeWriter
	once sync.Once
}

func (i *inbound) close(err error) {
	i.once.Do(func() {
		if err != nil {
			i.body.CloseWithError(err)
			return
		}
		i.body.Close()
	})
}

// New validates the options and prepares a client. It opens no connection; call Run for that.
func New(opts Options) (*Client, error) {
	if opts.APIKey == "" {
		return nil, errors.New("tunnelclient: APIKey is required")
	}
	if opts.ServerURL == "" {
		return nil, errors.New("tunnelclient: ServerURL is required")
	}
	opts.ServerURL = strings.TrimRight(opts.ServerURL, "/")
	if _, err := url.Parse(opts.ServerURL); err != nil {
		return nil, fmt.Errorf("tunnelclient: ServerURL %q: %w", opts.ServerURL, err)
	}

	var chosen int
	for _, set := range []bool{opts.Handler != nil, opts.TargetURL != "", opts.Dir != ""} {
		if set {
			chosen++
		}
	}
	if chosen != 1 {
		return nil, errors.New("tunnelclient: set exactly one of Handler, TargetURL and Dir")
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	c := &Client{opts: opts, logger: logger, streams: map[uint64]*inbound{}}

	// An http:// control plane sends the API key, and then every proxied request and response, in the clear.
	// That is reasonable against a loopback or test server and a mistake anywhere else, so it is said out loud rather than silently allowed.
	if u, err := url.Parse(opts.ServerURL); err == nil && u.Scheme != "https" && !isLoopback(u.Hostname()) {
		logger.Warn("the tunnel server url is not https, so the api key and all tunneled traffic travel unencrypted",
			"server_url", opts.ServerURL)
	}

	switch {
	case opts.Handler != nil:
		c.local = opts.Handler
	case opts.Dir != "":
		fs := newFileServer(opts.Dir, opts.CacheBytes, opts.DirectoryListing, logger)
		c.local, c.files = fs, fs
	default:
		target, err := url.Parse(opts.TargetURL)
		if err != nil {
			return nil, fmt.Errorf("tunnelclient: TargetURL %q: %w", opts.TargetURL, err)
		}
		if target.Scheme == "" || target.Host == "" {
			return nil, fmt.Errorf("tunnelclient: TargetURL %q needs a scheme and host, for example http://127.0.0.1:8756", opts.TargetURL)
		}
		c.target = target
		c.httpClient = &http.Client{
			Transport: &http.Transport{
				Proxy:                 nil, // Never route loopback traffic through a system proxy.
				DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
				MaxIdleConnsPerHost:   32,
				IdleConnTimeout:       90 * time.Second,
				ResponseHeaderTimeout: 0,
				TLSClientConfig:       &tls.Config{InsecureSkipVerify: opts.InsecureSkipVerify},
			},
			// Redirects belong to the caller, not to the tunnel.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	return c, nil
}

// Tunnel reports the live tunnel, or the zero value before the first connection succeeds.
func (c *Client) Tunnel() Tunnel {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current
}

// Describe names whatever this client exposes, for logs and banners.
func (c *Client) Describe() string {
	switch {
	case c.opts.Dir != "":
		if c.opts.CacheBytes > 0 {
			return fmt.Sprintf("%s (dir, %d MB cache)", c.opts.Dir, c.opts.CacheBytes>>20)
		}
		return c.opts.Dir + " (dir)"
	case c.target != nil:
		return c.target.String()
	default:
		return "an in process handler"
	}
}

// Run keeps a tunnel up until ctx is canceled, reconnecting with exponential backoff.
// It returns nil on cancellation, and an error only when the options are such that no attempt can ever succeed.
func (c *Client) Run(ctx context.Context) error {
	backoff := cmp.Or(c.opts.ReconnectMin, time.Second)
	maxBackoff := cmp.Or(c.opts.ReconnectMax, 30*time.Second)

	for {
		err := c.connectAndServe(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			c.logger.Warn("tunnel dropped", "err", err, "retry_in", backoff.String())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// connectAndServe registers a session, attaches the tunnel, and serves until it closes.
func (c *Client) connectAndServe(ctx context.Context) error {
	t, err := c.register(ctx)
	if err != nil {
		return err
	}
	conn, err := c.dialTunnel(ctx, t.Session)
	if err != nil {
		return err
	}
	defer conn.Close()

	c.mu.Lock()
	c.current = t
	c.mu.Unlock()

	c.logger.Info("tunnel established", "url", t.URL, "session", t.Session, "serving", c.Describe())
	if c.opts.OnConnect != nil {
		c.opts.OnConnect(t)
	}

	pingDone := make(chan struct{})
	defer close(pingDone)
	go c.pinger(conn, pingDone)
	if c.files != nil {
		go c.files.logStats(pingDone)
	}

	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	return c.serve(ctx, conn)
}

// register calls POST /api/v1/connect and returns the issued session.
func (c *Client) register(ctx context.Context) (Tunnel, error) {
	body, err := json.Marshal(map[string]string{
		"session_id":        c.opts.SessionID,
		"subdomain_request": c.opts.SubdomainRequest,
		"client_info":       cmp.Or(c.opts.ClientInfo, "https-tunnel client"),
	})
	if err != nil {
		return Tunnel{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.opts.ServerURL+"/api/v1/connect", strings.NewReader(string(body)))
	if err != nil {
		return Tunnel{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.opts.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Tunnel{}, fmt.Errorf("connecting to %s: %w", c.opts.ServerURL, err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return Tunnel{}, fmt.Errorf("connect rejected with %s: %s", resp.Status, strings.TrimSpace(string(payload)))
	}
	var out struct {
		Session string `json:"session"`
		URL     string `json:"url"`
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		return Tunnel{}, fmt.Errorf("parsing connect response: %w", err)
	}
	if out.Session == "" {
		return Tunnel{}, errors.New("server returned an empty session")
	}
	if out.Session != c.opts.SessionID && c.opts.OnSession != nil {
		if err := c.opts.OnSession(out.Session); err != nil {
			c.logger.Warn("could not persist the session id", "err", err)
		}
	}
	c.opts.SessionID = out.Session

	t := Tunnel{URL: out.URL, Session: out.Session}
	if u, err := url.Parse(out.URL); err == nil {
		t.Subdomain, _, _ = strings.Cut(u.Hostname(), ".")
	}
	return t, nil
}

// dialTunnel performs the upgrade handshake and returns the framed connection.
func (c *Client) dialTunnel(ctx context.Context, session string) (*tunnel.Conn, error) {
	u, err := url.Parse(c.opts.ServerURL)
	if err != nil {
		return nil, err
	}
	port := u.Port()
	useTLS := u.Scheme == "https"
	if port == "" {
		port = map[bool]string{true: "443", false: "80"}[useTLS]
	}
	addr := net.JoinHostPort(u.Hostname(), port)

	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	var conn net.Conn
	if useTLS {
		conn, err = (&tls.Dialer{
			NetDialer: dialer,
			Config:    &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12},
		}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("dialing %s: %w", addr, err)
	}
	if tcp, ok := underlyingTCP(conn); ok {
		_ = tcp.SetKeepAlive(true)
		_ = tcp.SetKeepAlivePeriod(30 * time.Second)
	}

	path := strings.TrimSuffix(u.Path, "/") + "/api/v1/tunnel"
	handshake := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\nX-Tunnel-Session: %s\r\nConnection: Upgrade\r\nUpgrade: %s\r\n\r\n",
		path, u.Host, c.opts.APIKey, session, tunnel.UpgradeProtocol)
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	if _, err := io.WriteString(conn, handshake); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReaderSize(conn, 32*1024)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("reading upgrade response: %w", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		conn.Close()
		return nil, fmt.Errorf("tunnel upgrade rejected with %s: %s", resp.Status, strings.TrimSpace(string(detail)))
	}
	_ = conn.SetDeadline(time.Time{})
	return tunnel.NewConn(conn, br), nil
}

// serve reads frames until the tunnel closes.
func (c *Client) serve(ctx context.Context, conn *tunnel.Conn) error {
	for {
		// The server pings every 30s, so silence well past that means a dead link.
		_ = conn.NetConn().SetReadDeadline(time.Now().Add(3 * time.Minute))
		f, err := conn.ReadFrame()
		if err != nil {
			c.closeAll(err)
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		switch f.Type {
		case tunnel.FrameHello:
			var hello tunnel.Hello
			if err := json.Unmarshal(f.Payload, &hello); err == nil && hello.Version != tunnel.ProtocolVersion {
				c.logger.Warn("protocol version mismatch", "server", hello.Version, "client", tunnel.ProtocolVersion)
			}
		case tunnel.FramePing:
			_ = conn.WriteFrame(tunnel.FramePong, tunnel.ControlStream, nil)
		case tunnel.FramePong:
			// Liveness only.
		case tunnel.FrameRequestHead:
			var head tunnel.RequestHead
			if err := json.Unmarshal(f.Payload, &head); err != nil {
				_ = conn.WriteJSON(tunnel.FrameAbort, f.Stream, tunnel.Abort{Message: "bad request head"})
				continue
			}
			pr, pw := io.Pipe()
			in := &inbound{body: pw}
			c.mu.Lock()
			c.streams[f.Stream] = in
			c.mu.Unlock()
			go c.handle(conn, f.Stream, head, pr)
		case tunnel.FrameRequestBody:
			if in := c.stream(f.Stream); in != nil {
				if _, err := in.body.Write(f.Payload); err != nil {
					in.close(err)
				}
			}
		case tunnel.FrameRequestEnd:
			if in := c.stream(f.Stream); in != nil {
				in.close(nil)
			}
		case tunnel.FrameAbort:
			if in := c.stream(f.Stream); in != nil {
				var a tunnel.Abort
				_ = json.Unmarshal(f.Payload, &a)
				in.close(errors.New(a.Message))
			}
			c.drop(f.Stream)
		default:
			c.logger.Debug("ignoring unexpected frame", "type", f.Type.String())
		}
	}
}

// handle answers one tunneled request, either in process or by proxying to the local target.
func (c *Client) handle(conn *tunnel.Conn, id uint64, head tunnel.RequestHead, body *io.PipeReader) {
	defer c.drop(id)

	if c.local != nil {
		c.serveLocally(conn, id, head, body)
		return
	}
	c.proxyToTarget(conn, id, head, body)
}

// serveLocally answers from the in process handler, writing the response into frames as the handler produces it.
func (c *Client) serveLocally(conn *tunnel.Conn, id uint64, head tunnel.RequestHead, body *io.PipeReader) {
	defer body.Close()

	req, err := http.NewRequest(head.Method, "http://"+cmp.Or(head.Host, "tunnel")+head.URI, body)
	if err != nil {
		c.fail(conn, id, http.StatusBadRequest, err.Error())
		return
	}
	for k, vs := range head.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	req.RemoteAddr = head.RemoteAddr
	if !head.HasBody {
		req.Body = http.NoBody
	}

	start := time.Now()
	fw := &frameWriter{conn: conn, id: id, header: http.Header{}}
	c.local.ServeHTTP(fw, req)
	fw.finish()
	c.logger.Info("served", "method", head.Method, "uri", head.URI, "status", fw.statusCode(),
		"cache", fw.header.Get("X-Cache"), "took", time.Since(start).Round(time.Millisecond).String())
}

// proxyToTarget replays the request against a server listening locally and streams the response back.
func (c *Client) proxyToTarget(conn *tunnel.Conn, id uint64, head tunnel.RequestHead, body *io.PipeReader) {
	target := *c.target
	parsed, err := url.ParseRequestURI(head.URI)
	if err != nil {
		c.fail(conn, id, http.StatusBadRequest, "bad request uri")
		return
	}
	target.Path = parsed.Path
	target.RawQuery = parsed.RawQuery

	var reqBody io.Reader
	if head.HasBody {
		reqBody = body
	} else {
		body.Close()
	}
	req, err := http.NewRequest(head.Method, target.String(), reqBody)
	if err != nil {
		c.fail(conn, id, http.StatusBadRequest, err.Error())
		return
	}
	for k, vs := range head.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	// The local server should see the hostname it is actually listening on, while the public name stays available in the forwarded headers.
	req.Host = target.Host
	req.Header.Del("Host")

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Warn("local request failed", "method", head.Method, "uri", head.URI, "err", err)
		c.fail(conn, id, http.StatusBadGateway, fmt.Sprintf("local server at %s: %v", c.target.Host, err))
		return
	}
	defer resp.Body.Close()

	if err := conn.WriteJSON(tunnel.FrameResponseHead, id, tunnel.ResponseHead{
		Status: resp.StatusCode,
		Header: resp.Header,
	}); err != nil {
		return
	}
	buf := make([]byte, 32*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if werr := conn.WriteBody(tunnel.FrameResponseBody, id, buf[:n]); werr != nil {
				return
			}
		}
		if rerr != nil {
			if !errors.Is(rerr, io.EOF) {
				_ = conn.WriteJSON(tunnel.FrameAbort, id, tunnel.Abort{Message: rerr.Error()})
				return
			}
			break
		}
	}
	_ = conn.WriteFrame(tunnel.FrameResponseEnd, id, nil)
	c.logger.Info("proxied", "method", head.Method, "uri", head.URI, "status", resp.StatusCode,
		"took", time.Since(start).Round(time.Millisecond).String())
}

// frameWriter is an http.ResponseWriter that writes a response into tunnel frames as it is produced, so a large file or a streaming handler is never buffered whole.
type frameWriter struct {
	conn        *tunnel.Conn
	id          uint64
	header      http.Header
	status      int
	wroteHeader bool
	err         error
}

func (f *frameWriter) Header() http.Header { return f.header }

func (f *frameWriter) WriteHeader(status int) {
	if f.wroteHeader {
		return
	}
	f.wroteHeader = true
	f.status = status
	f.err = f.conn.WriteJSON(tunnel.FrameResponseHead, f.id, tunnel.ResponseHead{
		Status: status,
		Header: f.header,
	})
}

func (f *frameWriter) Write(b []byte) (int, error) {
	if !f.wroteHeader {
		f.WriteHeader(http.StatusOK)
	}
	if f.err != nil {
		return 0, f.err
	}
	if f.err = f.conn.WriteBody(tunnel.FrameResponseBody, f.id, b); f.err != nil {
		return 0, f.err
	}
	return len(b), nil
}

// Flush satisfies http.Flusher; every frame is already flushed as it is written.
func (f *frameWriter) Flush() {}

func (f *frameWriter) finish() {
	if !f.wroteHeader {
		f.WriteHeader(http.StatusOK)
	}
	if f.err == nil {
		f.err = f.conn.WriteFrame(tunnel.FrameResponseEnd, f.id, nil)
	}
}

func (f *frameWriter) statusCode() int {
	if f.status == 0 {
		return http.StatusOK
	}
	return f.status
}

func (c *Client) fail(conn *tunnel.Conn, id uint64, status int, msg string) {
	_ = conn.WriteJSON(tunnel.FrameResponseHead, id, tunnel.ResponseHead{
		Status: status,
		Header: map[string][]string{"Content-Type": {"text/plain; charset=utf-8"}},
	})
	_ = conn.WriteBody(tunnel.FrameResponseBody, id, []byte(msg+"\n"))
	_ = conn.WriteFrame(tunnel.FrameResponseEnd, id, nil)
}

func (c *Client) pinger(conn *tunnel.Conn, done <-chan struct{}) {
	t := time.NewTicker(25 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			if err := conn.WriteFrame(tunnel.FramePing, tunnel.ControlStream, nil); err != nil {
				conn.Close()
				return
			}
		}
	}
}

func (c *Client) stream(id uint64) *inbound {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.streams[id]
}

func (c *Client) drop(id uint64) {
	c.mu.Lock()
	in := c.streams[id]
	delete(c.streams, id)
	c.mu.Unlock()
	if in != nil {
		in.close(nil)
	}
}

func (c *Client) closeAll(err error) {
	c.mu.Lock()
	streams := c.streams
	c.streams = map[uint64]*inbound{}
	c.mu.Unlock()
	for _, in := range streams {
		in.close(err)
	}
}

// isLoopback reports whether a host is this machine, where plaintext is not a real exposure.
func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func underlyingTCP(conn net.Conn) (*net.TCPConn, bool) {
	switch v := conn.(type) {
	case *net.TCPConn:
		return v, true
	case *tls.Conn:
		tcp, ok := v.NetConn().(*net.TCPConn)
		return tcp, ok
	default:
		return nil, false
	}
}
