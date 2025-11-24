package tunnelclient

import (
	"bytes"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// fileServer serves a directory over the tunnel, in place of proxying to a local port.
// It is a thin layer over http.ServeContent, which gives conditional requests, range requests and content type sniffing for free; the layer itself exists to answer from the LRU cache when it can.
type fileServer struct {
	root   string
	cache  *fileCache
	logger *slog.Logger
	// listing controls whether a directory without an index file is listed.
	listing bool
	// fallback serves directory listings, which are not worth caching.
	fallback http.Handler
}

func newFileServer(root string, cacheBytes int64, listing bool, logger *slog.Logger) *fileServer {
	return &fileServer{
		root:     root,
		cache:    newFileCache(cacheBytes),
		logger:   logger,
		listing:  listing,
		fallback: http.FileServer(http.Dir(root)),
	}
}

func (f *fileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "405 method not allowed: this tunnel serves a directory", http.StatusMethodNotAllowed)
		return
	}

	name, ok := f.resolve(r.URL.Path)
	if !ok {
		http.Error(w, "403 forbidden", http.StatusForbidden)
		return
	}

	info, err := os.Stat(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if info.IsDir() {
		// A directory with an index file serves it, exactly as a web server would.
		index := filepath.Join(name, "index.html")
		if indexInfo, err := os.Stat(index); err == nil && !indexInfo.IsDir() {
			if !strings.HasSuffix(r.URL.Path, "/") {
				http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
				return
			}
			f.serveFile(w, r, index, indexInfo)
			return
		}
		if !f.listing {
			http.Error(w, "403 forbidden: directory listing is off", http.StatusForbidden)
			return
		}
		f.fallback.ServeHTTP(w, r)
		return
	}

	f.serveFile(w, r, name, info)
}

func (f *fileServer) serveFile(w http.ResponseWriter, r *http.Request, name string, info os.FileInfo) {
	if data, ok := f.cache.get(name, info); ok {
		w.Header().Set("X-Cache", "hit")
		w.Header().Set("Content-Type", contentType(name, data))
		http.ServeContent(w, r, filepath.Base(name), info.ModTime(), bytes.NewReader(data))
		return
	}

	// Too big to hold, so it is streamed straight from the disk instead.
	if !f.cache.cacheable(info.Size()) {
		if f.cache.enabled() {
			w.Header().Set("X-Cache", "bypass")
		}
		if head, err := readHead(name); err == nil {
			w.Header().Set("Content-Type", contentType(name, head))
		}
		http.ServeFile(w, r, name)
		return
	}

	data, err := os.ReadFile(name)
	if err != nil {
		f.logger.Warn("could not read file", "path", name, "err", err)
		http.Error(w, "500 internal server error", http.StatusInternalServerError)
		return
	}
	// Stat again from the bytes just read, so a file edited mid-read is not cached under the older stamp.
	if fresh, err := os.Stat(name); err == nil {
		f.cache.put(name, data, fresh)
		info = fresh
	}
	w.Header().Set("X-Cache", "miss")
	w.Header().Set("Content-Type", contentType(name, data))
	http.ServeContent(w, r, filepath.Base(name), info.ModTime(), bytes.NewReader(data))
}

// sniffLength is how much of a file is examined to decide whether it is text.
// It matches what http.DetectContentType reads.
const sniffLength = 512

// readHead reads the first bytes of a file, for content type detection on the streaming path where the whole file is never held in memory.
func readHead(name string) ([]byte, error) {
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	head := make([]byte, sniffLength)
	n, err := file.Read(head)
	if err != nil && n == 0 {
		return nil, err
	}
	return head[:n], nil
}

// contentType decides what to label a file, preferring plain text for anything that reads as text.
//
// The extension alone cannot be trusted, and it is not even consistent between machines. mime.TypeByExtension reads the Windows registry, where ".mod" is a JVC camcorder video format, and /etc/mime.types on Linux, where ".md" is text/markdown and ".ts" is a Qt Linguist file; a browser downloads all three.
// So when a file's bytes look like text, the extension's type is gonored only if a browser would actually render it, and anything else becomes text/plain.
// Binary files keep whatever the extension says, falling back to sniffing.
func contentType(name string, head []byte) string {
	if len(head) > sniffLength {
		head = head[:sniffLength]
	}
	byExt := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))

	if looksLikeText(head) {
		if byExt != "" && rendersAsText(byExt) {
			return byExt
		}
		return "text/plain; charset=utf-8"
	}
	if byExt != "" {
		return byExt
	}
	return http.DetectContentType(head)
}

// rendersAsText reports whether a browser displays a media type inline rather than downloading it.
//
// The list is short on purpose. "text/" is not a good enough test: a Debian box maps .md to text/markdown and .ts to text/vnd.trolltech.linguist, and every browser downloads both, which is the behavior this whole path exists to avoid.
// Anything outside this set that reads as text is relabeled text/plain, so a readable file opens in the browser whatever the local mime table happens to call it.
func rendersAsText(mediaType string) bool {
	base, _, err := mime.ParseMediaType(mediaType)
	if err != nil {
		base = mediaType
	}
	switch base {
	case "text/plain", "text/html", "text/css", "text/javascript", "text/xml",
		"application/json", "application/javascript", "application/xml",
		"application/xhtml+xml", "application/ld+json", "image/svg+xml":
		return true
	}
	return false
}

// looksLikeText reports whether a chunk of a file reads as human readable text.
// A NUL byte or invalid UTF-8 means binary; so does a sprinkling of other control characters, which is what separates a source file from a compressed one that happens to decode.
func looksLikeText(head []byte) bool {
	if len(head) == 0 {
		return true // An empty file is harmless to show.
	}
	if !utf8.Valid(head) {
		return false
	}
	var control int
	for _, r := range string(head) {
		if r == 0 {
			return false
		}
		if r == '\t' || r == '\n' || r == '\r' {
			continue // Ordinary in text.
		}
		if r < 0x20 || r == 0x7f {
			control++
		}
	}
	// One stray control character in 512 bytes is tolerable; a scattering is not.
	return control*100 <= len(head)
}

// resolve maps a request path onto a path inside the root, refusing anything that climbs out of it.
func (f *fileServer) resolve(urlPath string) (string, bool) {
	if !strings.HasPrefix(urlPath, "/") {
		urlPath = "/" + urlPath
	}
	clean := filepath.Clean(filepath.FromSlash(urlPath))
	joined := filepath.Join(f.root, clean)

	// Compare resolved paths, so a symlink pointing outside the root is refused as well as a ../ traversal.
	rootAbs, err := filepath.EvalSymlinks(f.root)
	if err != nil {
		rootAbs, err = filepath.Abs(f.root)
		if err != nil {
			return "", false
		}
	}
	target := joined
	if resolved, err := filepath.EvalSymlinks(joined); err == nil {
		target = resolved
	} else if abs, err := filepath.Abs(joined); err == nil {
		target = abs
	}
	rel, err := filepath.Rel(rootAbs, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return joined, true
}

// logStats reports cache effectiveness on a slow ticker, so a long running tunnel shows whether the budget is doing anything.
func (f *fileServer) logStats(stop <-chan struct{}) {
	if !f.cache.enabled() {
		return
	}
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			s := f.cache.stats()
			if s.Hits+s.Misses == 0 {
				continue
			}
			f.logger.Info("file cache",
				"entries", s.Entries,
				"used_mb", float64(s.UsedBytes)/(1<<20),
				"max_mb", float64(s.MaxBytes)/(1<<20),
				"hits", s.Hits, "misses", s.Misses, "evictions", s.Evictions)
		}
	}
}
