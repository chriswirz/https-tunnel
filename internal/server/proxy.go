package server

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/chriswirz/https-tunnel/internal/tunnel"
)

// hopByHopHeaders are connection-scoped and must not be forwarded through the tunnel.
var hopByHopHeaders = []string{
	"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
	"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

// serveTunnel proxies one public request over the session's tunnel to the client's local server.
func (s *Server) serveTunnel(w http.ResponseWriter, r *http.Request, sess *Session) {
	st, conn, err := sess.newStream()
	if err != nil {
		s.renderOffline(w, r, sess)
		return
	}
	defer sess.dropStream(st.id)

	head := tunnel.RequestHead{
		Method:     r.Method,
		URI:        r.URL.RequestURI(),
		Proto:      r.Proto,
		Host:       r.Host,
		Header:     sanitizeHeader(r.Header),
		RemoteAddr: clientIP(r, s.cfg.TrustForwardedHeaders),
		HasBody:    r.Body != nil && r.ContentLength != 0,
	}
	appendForwarded(head.Header, r, clientIP(r, s.cfg.TrustForwardedHeaders), s.scheme(r))

	if err := conn.WriteJSON(tunnel.FrameRequestHead, st.id, head); err != nil {
		s.renderOffline(w, r, sess)
		return
	}

	// Pump the request body to the client while we wait for response headers, so streaming request bodies (long-lived MCP POSTs) are not buffered here.
	bodyDone := make(chan error, 1)
	go func() {
		bodyDone <- pumpRequestBody(conn, st.id, r.Body)
	}()

	ctx := r.Context()
	var respHead tunnel.ResponseHead
	select {
	case respHead = <-st.head:
	case err := <-st.err:
		s.logger.Warn("tunnel request failed", "session", sess.ID, "err", err)
		s.renderOffline(w, r, sess)
		return
	case <-ctx.Done():
		_ = conn.WriteJSON(tunnel.FrameAbort, st.id, tunnel.Abort{Message: "client went away"})
		return
	case <-time.After(s.responseTimeout):
		_ = conn.WriteJSON(tunnel.FrameAbort, st.id, tunnel.Abort{Message: "timeout waiting for local server"})
		http.Error(w, "504 gateway timeout: the tunneled server did not respond", http.StatusGatewayTimeout)
		return
	}

	dst := w.Header()
	for k, vs := range respHead.Header {
		if isHopByHop(k) {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
	status := respHead.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)

	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, err := st.reader.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				_ = conn.WriteJSON(tunnel.FrameAbort, st.id, tunnel.Abort{Message: "downstream write failed"})
				return
			}
			// Flush every chunk so server-sent events and other streaming responses reach the caller as the local server produces them.
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.logger.Debug("tunnel response ended", "session", sess.ID, "err", err)
			}
			break
		}
	}
	<-bodyDone
}

func pumpRequestBody(conn *tunnel.Conn, id uint64, body io.ReadCloser) error {
	defer func() { _ = conn.WriteFrame(tunnel.FrameRequestEnd, id, nil) }()
	if body == nil {
		return nil
	}
	defer body.Close()
	buf := make([]byte, 32*1024)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			if werr := conn.WriteFrame(tunnel.FrameRequestBody, id, buf[:n]); werr != nil {
				return werr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// readLoop consumes frames from an attached client until the connection dies.
func (s *Server) readLoop(sess *Session, conn *tunnel.Conn) {
	defer sess.detach(conn)
	for {
		_ = conn.NetConn().SetReadDeadline(time.Now().Add(s.readTimeout))
		f, err := conn.ReadFrame()
		if err != nil {
			s.logger.Info("tunnel closed", "session", sess.ID, "err", err)
			return
		}
		switch f.Type {
		case tunnel.FramePing:
			_ = conn.WriteFrame(tunnel.FramePong, tunnel.ControlStream, nil)
		case tunnel.FramePong:
			// Liveness only.
		case tunnel.FrameResponseHead:
			st := sess.stream(f.Stream)
			if st == nil {
				continue
			}
			var head tunnel.ResponseHead
			if err := json.Unmarshal(f.Payload, &head); err != nil {
				st.finish(err)
				continue
			}
			select {
			case st.head <- head:
			default:
			}
		case tunnel.FrameResponseBody:
			if st := sess.stream(f.Stream); st != nil {
				if _, err := st.body.Write(f.Payload); err != nil {
					_ = conn.WriteJSON(tunnel.FrameAbort, f.Stream, tunnel.Abort{Message: err.Error()})
				}
			}
		case tunnel.FrameResponseEnd:
			if st := sess.stream(f.Stream); st != nil {
				st.finish(nil)
			}
		case tunnel.FrameAbort:
			if st := sess.stream(f.Stream); st != nil {
				var a tunnel.Abort
				_ = json.Unmarshal(f.Payload, &a)
				st.finish(errors.New(cmpOr(a.Message, "aborted by client")))
			}
		default:
			s.logger.Debug("ignoring unexpected frame", "type", f.Type.String())
		}
	}
}

// keepalive pings the client so dead connections are noticed promptly.
func (s *Server) keepalive(sess *Session, conn *tunnel.Conn, done <-chan struct{}) {
	t := time.NewTicker(s.pingInterval)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-s.shutdown:
			return
		case <-t.C:
			if err := conn.WriteFrame(tunnel.FramePing, tunnel.ControlStream, nil); err != nil {
				sess.detach(conn)
				return
			}
		}
	}
}

func sanitizeHeader(h http.Header) map[string][]string {
	out := make(map[string][]string, len(h))
	for k, v := range h {
		if isHopByHop(k) {
			continue
		}
		vs := make([]string, len(v))
		copy(vs, v)
		out[k] = vs
	}
	return out
}

func isHopByHop(k string) bool {
	for _, h := range hopByHopHeaders {
		if strings.EqualFold(k, h) {
			return true
		}
	}
	return false
}

func appendForwarded(h map[string][]string, r *http.Request, ip, scheme string) {
	if prior, ok := h["X-Forwarded-For"]; ok && len(prior) > 0 {
		h["X-Forwarded-For"] = []string{strings.Join(prior, ", ") + ", " + ip}
	} else {
		h["X-Forwarded-For"] = []string{ip}
	}
	h["X-Forwarded-Proto"] = []string{scheme}
	h["X-Forwarded-Host"] = []string{r.Host}
}

func clientIP(r *http.Request, trustForwarded bool) string {
	if trustForwarded {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if first, _, ok := strings.Cut(xff, ","); ok {
				return strings.TrimSpace(first)
			}
			return strings.TrimSpace(xff)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
