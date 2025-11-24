package tunnel

import (
	"bytes"
	"errors"
	"net"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	ca, cb := NewConn(a, nil), NewConn(b, nil)

	body := bytes.Repeat([]byte("x"), 4096)
	go func() {
		_ = ca.WriteJSON(FrameRequestHead, 7, RequestHead{Method: "POST", URI: "/mcp?x=1"})
		_ = ca.WriteFrame(FrameRequestBody, 7, body)
		_ = ca.WriteFrame(FrameRequestEnd, 7, nil)
	}()

	f, err := cb.ReadFrame()
	if err != nil || f.Type != FrameRequestHead || f.Stream != 7 {
		t.Fatalf("head frame: %+v err=%v", f, err)
	}
	if f, err = cb.ReadFrame(); err != nil || !bytes.Equal(f.Payload, body) {
		t.Fatalf("body frame: len=%d err=%v", len(f.Payload), err)
	}
	if f, err = cb.ReadFrame(); err != nil || f.Type != FrameRequestEnd || len(f.Payload) != 0 {
		t.Fatalf("end frame: %+v err=%v", f, err)
	}
}

func TestWriteBodySplitsOversizedPayloads(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	ca, cb := NewConn(a, nil), NewConn(b, nil)

	payload := bytes.Repeat([]byte("y"), MaxPayload+1234)
	go func() { _ = ca.WriteBody(FrameResponseBody, 1, payload) }()

	var got []byte
	for len(got) < len(payload) {
		f, err := cb.ReadFrame()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if len(f.Payload) > MaxPayload {
			t.Fatalf("frame of %d bytes exceeds the cap", len(f.Payload))
		}
		got = append(got, f.Payload...)
	}
	if !bytes.Equal(got, payload) {
		t.Error("reassembled payload does not match")
	}
}

func TestWriteFrameRejectsOversizedSingleFrame(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	c := NewConn(a, nil)
	err := c.WriteFrame(FrameResponseBody, 1, make([]byte, MaxPayload+1))
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("got %v, want ErrFrameTooLarge", err)
	}
}
