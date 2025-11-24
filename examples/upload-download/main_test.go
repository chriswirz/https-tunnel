package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// A file is served with the type it actually is, and rendered inline when that is safe.
func TestDownloadContentType(t *testing.T) {
	root := t.TempDir()
	// A one pixel GIF, so the sniffing path has real bytes to work with.
	gif := []byte("GIF89a\x01\x00\x01\x00\x00\xff\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x00;")

	cases := []struct {
		file        string
		body        []byte
		ctype       string
		disposition string
	}{
		{"notes.txt", []byte("hello"), "text/plain; charset=utf-8", "inline"},
		{"rows.csv", []byte("a,b\n1,2\n"), "text/plain; charset=utf-8", "inline"},
		{"pixel.gif", gif, "image/gif", "inline"},
		{"noextension", gif, "image/gif", "inline"}, // sniffed, not named
		{"data.json", []byte(`{"a":1}`), "application/json", "inline"},
		// Script bearing types stay attachments: the tunnel serves them from the site's own origin.
		{"page.html", []byte("<h1>hi</h1>"), "text/html; charset=utf-8", "attachment"},
		{"vector.svg", []byte("<svg/>"), "image/svg+xml", "attachment"},
		{"bundle.zip", []byte("PK\x03\x04rest"), "application/zip", "attachment"},
	}

	for _, c := range cases {
		if err := os.WriteFile(filepath.Join(root, c.file), c.body, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	a, err := newApp(root, "pw", "", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	id, err := a.newSession()
	if err != nil {
		t.Fatal(err)
	}

	get := func(path string) *http.Response {
		req := httptest.NewRequest("GET", path, nil)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: id})
		rec := httptest.NewRecorder()
		a.ServeHTTP(rec, req)
		return rec.Result()
	}

	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			res := get("/files/" + c.file)
			if res.StatusCode != http.StatusOK {
				t.Fatalf("status %d", res.StatusCode)
			}
			if got := res.Header.Get("Content-Type"); got != c.ctype {
				t.Errorf("Content-Type = %q, want %q", got, c.ctype)
			}
			want := c.disposition + "; filename*=UTF-8''" + c.file
			if got := res.Header.Get("Content-Disposition"); got != want {
				t.Errorf("Content-Disposition = %q, want %q", got, want)
			}
			if got := res.Header.Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q", got)
			}

			// Sniffing reads the first bytes; the body must still arrive whole.
			body, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(body, c.body) {
				t.Errorf("body = %q, want %q", body, c.body)
			}

			// ?download=1 forces the save even for a type that would otherwise open in a tab.
			res = get("/files/" + c.file + "?download=1")
			if got := res.Header.Get("Content-Disposition"); got[:len("attachment")] != "attachment" {
				t.Errorf("forced download gave %q", got)
			}
		})
	}
}
