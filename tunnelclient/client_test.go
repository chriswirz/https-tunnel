package tunnelclient

import (
	"mime"
	"net/http"
	"strings"
	"testing"
)

func TestNewRejectsIncompleteOptions(t *testing.T) {
	handler := http.NewServeMux()
	cases := map[string]Options{
		"no api key":     {ServerURL: "https://tunnel.example.com", Handler: handler},
		"no server url":  {APIKey: "k", Handler: handler},
		"nothing served": {APIKey: "k", ServerURL: "https://tunnel.example.com"},
		"two targets": {
			APIKey: "k", ServerURL: "https://tunnel.example.com",
			Handler: handler, Dir: t.TempDir(),
		},
		"target without a scheme": {
			APIKey: "k", ServerURL: "https://tunnel.example.com", TargetURL: "127.0.0.1:8756",
		},
	}
	for name, opts := range cases {
		if _, err := New(opts); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestNewAcceptsEachTargetKind(t *testing.T) {
	base := Options{APIKey: "k", ServerURL: "https://tunnel.example.com/"}

	handlerOpts := base
	handlerOpts.Handler = http.NewServeMux()

	dirOpts := base
	dirOpts.Dir = t.TempDir()
	dirOpts.CacheBytes = 8 << 20

	targetOpts := base
	targetOpts.TargetURL = "http://127.0.0.1:8756"

	for name, opts := range map[string]Options{"handler": handlerOpts, "dir": dirOpts, "target": targetOpts} {
		c, err := New(opts)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if c.Describe() == "" {
			t.Errorf("%s: Describe is empty", name)
		}
		if got := c.Tunnel(); got.URL != "" {
			t.Errorf("%s: a client that has not connected reports %q", name, got.URL)
		}
		// The trailing slash on ServerURL must not survive into request paths.
		if strings.HasSuffix(c.opts.ServerURL, "/") {
			t.Errorf("%s: ServerURL kept its trailing slash", name)
		}
	}
}

func TestContentTypePrefersTextForReadableFiles(t *testing.T) {
	cases := []struct {
		name string
		file string
		head []byte
		want string
	}{
		// The local mime table decides none of these. Windows calls .mod a
		// camcorder video, Debian calls .md text/markdown and .ts a Qt Linguist
		// file, and browsers download all three, so a readable file is
		// relabeled text/plain on every platform.
		{"go.mod", "go.mod", []byte("module example.com/x\n\ngo 1.25\n"), "text/plain; charset=utf-8"},
		{"markdown", "readme.md", []byte("# Title\n"), "text/plain; charset=utf-8"},
		{"typescript", "app.ts", []byte("const x: number = 1;\n"), "text/plain; charset=utf-8"},
		{"go source", "main.go", []byte("package main\n"), "text/plain; charset=utf-8"},
		{"yaml", "conf.yaml", []byte("key: value\n"), "text/plain; charset=utf-8"},
		{"no extension", "LICENSE", []byte("MIT License\n"), "text/plain; charset=utf-8"},

		// Types a browser really does render are kept, because they say more
		// than text/plain does.
		{"html", "page.html", []byte("<h1>hi</h1>"), "text/html; charset=utf-8"},
		{"json", "data.json", []byte(`{"a":1}`), "application/json"},
		{"css", "style.css", []byte("body{}"), "text/css; charset=utf-8"},

		// Binary keeps whatever the extension says.
		{"png", "icon.png", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), "image/png"},
	}
	for _, tc := range cases {
		if got := contentType(tc.file, tc.head); got != tc.want {
			t.Errorf("%s: contentType(%q) = %q, want %q", tc.name, tc.file, got, tc.want)
		}
	}
}

// The mime table differs per machine, so the decision that depends on it is
// tested directly, with the values other platforms actually produce. The first
// two are what broke the build on linux.
func TestRendersAsText(t *testing.T) {
	renders := []string{
		"text/plain; charset=utf-8",
		"text/html; charset=utf-8",
		"text/css",
		"text/javascript",
		"application/json",
		"application/xml",
		"image/svg+xml",
	}
	for _, mediaType := range renders {
		if !rendersAsText(mediaType) {
			t.Errorf("%q renders in a browser and should be kept", mediaType)
		}
	}

	downloads := []string{
		"text/markdown; charset=utf-8",               // .md on debian
		"text/vnd.trolltech.linguist; charset=utf-8", // .ts on debian
		"video/mpeg", // .mod on windows
		"text/x-go",
		"text/csv",
		"application/octet-stream",
		"image/png",
	}
	for _, mediaType := range downloads {
		if rendersAsText(mediaType) {
			t.Errorf("%q is downloaded by browsers, so it must not count as renderable", mediaType)
		}
	}
}

func TestLooksLikeText(t *testing.T) {
	if !looksLikeText([]byte("plain text\twith\ttabs\r\n")) {
		t.Error("tabs and newlines should read as text")
	}
	if !looksLikeText(nil) {
		t.Error("an empty file should read as text")
	}
	if looksLikeText([]byte("has a \x00 nul")) {
		t.Error("a NUL byte means binary")
	}
	if looksLikeText([]byte{0xff, 0xfe, 0xfd, 0xfc}) {
		t.Error("invalid UTF-8 means binary")
	}
	if looksLikeText([]byte("\x01\x02\x03\x04\x05\x06\x07\x08")) {
		t.Error("a run of control characters means binary")
	}
}

// Registering the mappings a Debian machine gets from /etc/mime.types makes the
// linux behavior reproducible from any platform. Without the rule in
// contentType these two come back as text/markdown and a Qt Linguist type, and
// a browser downloads both.
func TestContentTypeWithLinuxMimeTable(t *testing.T) {
	for ext, mediaType := range map[string]string{
		".md": "text/markdown; charset=utf-8",
		".ts": "text/vnd.trolltech.linguist; charset=utf-8",
	} {
		if err := mime.AddExtensionType(ext, mediaType); err != nil {
			t.Fatalf("registering %s: %v", ext, err)
		}
		if got := mime.TypeByExtension(ext); got != mediaType {
			t.Skipf("this platform pins %s to %q, so the linux table cannot be simulated", ext, got)
		}
	}

	const want = "text/plain; charset=utf-8"
	if got := contentType("readme.md", []byte("# Title\n")); got != want {
		t.Errorf("readme.md = %q, want %q", got, want)
	}
	if got := contentType("app.ts", []byte("const x = 1;\n")); got != want {
		t.Errorf("app.ts = %q, want %q", got, want)
	}
}
