// Package tunnel defines the framing protocol spoken over a hijacked HTTP connection between an https-tunnel client and server.
//
// The client performs an HTTP/1.1 upgrade handshake ("Upgrade: https-tunnel") against the server's control plane.
// Once the server answers 101, both sides stop speaking HTTP and exchange binary frames instead.
// Using an upgrade rather than a bespoke port means nginx (and most other front proxies) will tunnel the connection untouched, exactly as it does for WebSockets.
//
// Wire format, big endian:
//
//	+--------+------------------+------------+-----------------+
//	| type:1 | stream id: 8     | length: 4  | payload: length |
//	+--------+------------------+------------+-----------------+
//
// Stream id 0 is the control stream (hello, ping, pong).
// Every proxied HTTP request gets its own odd, monotonically increasing stream id allocated by the server, which is the only side that originates requests.
package tunnel

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// UpgradeProtocol is the token used in the Upgrade header of the handshake.
const UpgradeProtocol = "https-tunnel"

// ProtocolVersion is bumped on incompatible wire changes.
const ProtocolVersion = 1

// MaxPayload caps a single frame's payload.
// Bodies larger than this are split across multiple frames.
const MaxPayload = 1 << 20

// ControlStream carries connection-scoped frames.
const ControlStream uint64 = 0

// FrameType identifies the meaning of a frame's payload.
type FrameType uint8

// Frame types.
const (
	FrameHello        FrameType = 1 // server -> client, payload: Hello
	FrameRequestHead  FrameType = 2 // server -> client, payload: RequestHead
	FrameRequestBody  FrameType = 3 // server -> client, payload: raw bytes
	FrameRequestEnd   FrameType = 4 // server -> client, no payload
	FrameResponseHead FrameType = 5 // client -> server, payload: ResponseHead
	FrameResponseBody FrameType = 6 // client -> server, payload: raw bytes
	FrameResponseEnd  FrameType = 7 // client -> server, no payload
	FrameAbort        FrameType = 8 // either direction, payload: Abort
	FramePing         FrameType = 9
	FramePong         FrameType = 10
)

func (t FrameType) String() string {
	switch t {
	case FrameHello:
		return "hello"
	case FrameRequestHead:
		return "request-head"
	case FrameRequestBody:
		return "request-body"
	case FrameRequestEnd:
		return "request-end"
	case FrameResponseHead:
		return "response-head"
	case FrameResponseBody:
		return "response-body"
	case FrameResponseEnd:
		return "response-end"
	case FrameAbort:
		return "abort"
	case FramePing:
		return "ping"
	case FramePong:
		return "pong"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(t))
	}
}

// Frame is a single protocol message.
type Frame struct {
	Type    FrameType
	Stream  uint64
	Payload []byte
}

// Hello is sent by the server immediately after the upgrade completes.
type Hello struct {
	Version   int    `json:"version"`
	Session   string `json:"session"`
	URL       string `json:"url"`
	ServerNow int64  `json:"server_now"`
}

// RequestHead describes an inbound public HTTP request.
type RequestHead struct {
	Method     string              `json:"method"`
	URI        string              `json:"uri"` // path + raw query
	Proto      string              `json:"proto"`
	Host       string              `json:"host"`
	Header     map[string][]string `json:"header"`
	RemoteAddr string              `json:"remote_addr"`
	HasBody    bool                `json:"has_body"`
}

// ResponseHead describes the local server's answer.
type ResponseHead struct {
	Status int                 `json:"status"`
	Header map[string][]string `json:"header"`
}

// Abort terminates a stream early, in either direction.
type Abort struct {
	Message string `json:"message,omitempty"`
}

// ErrFrameTooLarge is returned when a peer announces an oversized payload.
var ErrFrameTooLarge = errors.New("tunnel: frame payload exceeds maximum")

// Conn is a framed, concurrency-safe connection over a hijacked net.Conn.
type Conn struct {
	conn net.Conn
	r    *bufio.Reader

	wmu sync.Mutex
	w   *bufio.Writer

	// WriteTimeout bounds a single frame write so a stalled peer cannot wedge the writer lock forever.
	WriteTimeout time.Duration
}

// NewConn wraps a hijacked connection.
// Any bytes already buffered by the HTTP server must be passed in as r so they are not lost.
func NewConn(c net.Conn, r *bufio.Reader) *Conn {
	if r == nil {
		r = bufio.NewReaderSize(c, 32*1024)
	}
	return &Conn{
		conn:         c,
		r:            r,
		w:            bufio.NewWriterSize(c, 32*1024),
		WriteTimeout: 30 * time.Second,
	}
}

// NetConn exposes the underlying connection, mainly for read deadlines.
func (c *Conn) NetConn() net.Conn { return c.conn }

// ReadFrame reads the next frame.
// It is not safe for concurrent use; a single reader goroutine per connection is expected.
func (c *Conn) ReadFrame() (Frame, error) {
	var hdr [13]byte
	if _, err := io.ReadFull(c.r, hdr[:]); err != nil {
		return Frame{}, err
	}
	f := Frame{
		Type:   FrameType(hdr[0]),
		Stream: binary.BigEndian.Uint64(hdr[1:9]),
	}
	n := binary.BigEndian.Uint32(hdr[9:13])
	if n > MaxPayload {
		return Frame{}, ErrFrameTooLarge
	}
	if n > 0 {
		f.Payload = make([]byte, n)
		if _, err := io.ReadFull(c.r, f.Payload); err != nil {
			return Frame{}, err
		}
	}
	return f, nil
}

// WriteFrame writes one frame and flushes it.
func (c *Conn) WriteFrame(t FrameType, stream uint64, payload []byte) error {
	if len(payload) > MaxPayload {
		return ErrFrameTooLarge
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.WriteTimeout > 0 {
		_ = c.conn.SetWriteDeadline(time.Now().Add(c.WriteTimeout))
		defer c.conn.SetWriteDeadline(time.Time{})
	}
	var hdr [13]byte
	hdr[0] = byte(t)
	binary.BigEndian.PutUint64(hdr[1:9], stream)
	binary.BigEndian.PutUint32(hdr[9:13], uint32(len(payload)))
	if _, err := c.w.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := c.w.Write(payload); err != nil {
			return err
		}
	}
	return c.w.Flush()
}

// WriteJSON marshals v and writes it as a single frame.
func (c *Conn) WriteJSON(t FrameType, stream uint64, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.WriteFrame(t, stream, b)
}

// WriteBody splits b into MaxPayload-sized frames of the given type.
func (c *Conn) WriteBody(t FrameType, stream uint64, b []byte) error {
	for len(b) > MaxPayload {
		if err := c.WriteFrame(t, stream, b[:MaxPayload]); err != nil {
			return err
		}
		b = b[MaxPayload:]
	}
	return c.WriteFrame(t, stream, b)
}

// Close closes the underlying connection.
func (c *Conn) Close() error { return c.conn.Close() }
