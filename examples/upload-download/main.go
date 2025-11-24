// Command upload-download publishes a private file drop on a public HTTPS URL.
//
// It is the tunnel client used as a library with an http.Handler: the whole site, web assets
// included, is embedded in the binary, so the only thing on disk is the folder the files live in.
// Visitors have to sign in with the admin password before they can list, upload, download or
// delete anything.
//
//	go run ./examples/upload-download \
//	  -server   https://tunnel.example.com \
//	  -key      your-api-key \
//	  -password hunter2 \
//	  -dir      ./drop
//
// Add -subdomain to ask for a particular name, and -session to reclaim a URL a previous run was given.
package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chriswirz/https-tunnel/tunnelclient"
)

//go:embed web
var webFS embed.FS

// extTypes pins the types that matter most for viewing a file in the browser. The standard library
// falls back to the host's own table, which on Windows comes from the registry and is happy to call
// a .csv a spreadsheet or leave a .md unknown, so the common cases are spelled out here instead.
var extTypes = map[string]string{
	".txt":  "text/plain; charset=utf-8",
	".log":  "text/plain; charset=utf-8",
	".md":   "text/plain; charset=utf-8",
	".csv":  "text/plain; charset=utf-8",
	".json": "application/json",
	".yaml": "text/plain; charset=utf-8",
	".yml":  "text/plain; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".js":   "text/javascript; charset=utf-8",
	".html": "text/html; charset=utf-8",
	".htm":  "text/html; charset=utf-8",
	".svg":  "image/svg+xml",
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".avif": "image/avif",
	".bmp":  "image/bmp",
	".ico":  "image/x-icon",
	".pdf":  "application/pdf",
	".mp3":  "audio/mpeg",
	".wav":  "audio/wav",
	".mp4":  "video/mp4",
	".webm": "video/webm",
	".zip":  "application/zip",
}

const (
	sessionCookie = "ud_session"
	sessionTTL    = 12 * time.Hour
)

func main() {
	var (
		serverURL = flag.String("server", "", "tunnel server, e.g. https://tunnel.example.com")
		apiKey    = flag.String("key", os.Getenv("TUNNEL_API_KEY"), "api key, or set TUNNEL_API_KEY")
		password  = flag.String("password", os.Getenv("ADMIN_PASSWORD"), "admin password, or set ADMIN_PASSWORD")
		dir       = flag.String("dir", ".", "directory files are uploaded to and served from")
		title     = flag.String("title", "File drop", "heading and browser tab title for the site")
		maxMB     = flag.Int64("max-mb", 512, "largest upload accepted, in megabytes")
		subdomain = flag.String("subdomain", "", "optional subdomain to request")
		sessionID = flag.String("session", "", "optional session id to resume, keeping the same url")
		sessFile  = flag.String("session-file", "", "optional file to read and write the session id, so restarts keep the url")
		verbose   = flag.Bool("v", false, "log every request")
	)
	flag.Parse()

	if *serverURL == "" || *apiKey == "" {
		fmt.Fprintln(os.Stderr, "both -server and -key (or TUNNEL_API_KEY) are required")
		os.Exit(2)
	}
	if *password == "" {
		fmt.Fprintln(os.Stderr, "-password (or ADMIN_PASSWORD) is required; anyone with the URL can reach the login page")
		os.Exit(2)
	}

	root, err := filepath.Abs(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	level := slog.LevelWarn
	if *verbose {
		level = slog.LevelInfo
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	site, err := newApp(root, *password, *title, *maxMB<<20)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// A session id kept on disk is what makes the URL survive a restart.
	session := *sessionID
	if session == "" && *sessFile != "" {
		if saved, err := os.ReadFile(*sessFile); err == nil {
			session = string(saved)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tc, err := tunnelclient.New(tunnelclient.Options{
		APIKey:           *apiKey,
		ServerURL:        *serverURL,
		SessionID:        session,
		SubdomainRequest: *subdomain,

		// Handler serves every request in process, so nothing listens locally.
		Handler: site,

		Logger:     logger,
		ClientInfo: "upload-download example",
		OnSession: func(id string) error {
			if *sessFile == "" {
				return nil
			}
			return os.WriteFile(*sessFile, []byte(id), 0o600)
		},
		OnConnect: func(t tunnelclient.Tunnel) {
			fmt.Printf("\n  %s  ->  %s\n  session: %s\n\n", t.URL, root, t.Session)
			fmt.Println("  sign in with the admin password to upload, download and delete")
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if err := tc.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// app is the whole site: a login page, a file list, and upload, download and delete endpoints.
type app struct {
	root     string
	password string
	title    string
	maxBytes int64

	mux    *http.ServeMux
	pages  *template.Template
	assets http.Handler

	mu       sync.Mutex
	sessions map[string]time.Time
}

func newApp(root, password, title string, maxBytes int64) (*app, error) {
	pages, err := template.ParseFS(webFS, "web/*.html")
	if err != nil {
		return nil, err
	}
	static, err := fs.Sub(webFS, "web")
	if err != nil {
		return nil, err
	}

	if title == "" {
		title = "File drop"
	}

	a := &app{
		root:     root,
		password: password,
		title:    title,
		maxBytes: maxBytes,
		pages:    pages,
		assets:   http.FileServer(http.FS(static)),
		sessions: map[string]time.Time{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", a.index)
	mux.HandleFunc("GET /login", a.loginPage)
	mux.HandleFunc("POST /login", a.login)
	mux.HandleFunc("POST /logout", a.logout)
	mux.Handle("GET /favicon.ico", a.assets)
	mux.Handle("GET /assets/", http.StripPrefix("/assets", a.assets))
	mux.HandleFunc("GET /api/files", a.guard(a.listFiles))
	mux.HandleFunc("POST /api/upload", a.guard(a.upload))
	mux.HandleFunc("POST /api/delete", a.guard(a.deleteFile))
	mux.HandleFunc("GET /files/{name}", a.guard(a.download))
	a.mux = mux
	return a, nil
}

func (a *app) ServeHTTP(w http.ResponseWriter, r *http.Request) { a.mux.ServeHTTP(w, r) }

// --- sessions ---

func (a *app) newSession() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	id := hex.EncodeToString(buf)
	a.mu.Lock()
	defer a.mu.Unlock()
	for old, exp := range a.sessions {
		if time.Now().After(exp) {
			delete(a.sessions, old)
		}
	}
	a.sessions[id] = time.Now().Add(sessionTTL)
	return id, nil
}

func (a *app) authed(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	exp, ok := a.sessions[c.Value]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(a.sessions, c.Value)
		return false
	}
	return true
}

// guard turns away anyone without a live session: an API call gets 401, a page gets the login form.
func (a *app) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.authed(r) {
			next(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not signed in"})
			return
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}
}

// --- pages ---

func (a *app) index(w http.ResponseWriter, r *http.Request) {
	if !a.authed(r) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	a.render(w, "index.html", a.data(nil))
}

func (a *app) loginPage(w http.ResponseWriter, r *http.Request) {
	if a.authed(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	a.render(w, "login.html", a.data(map[string]any{"Error": r.URL.Query().Get("error") != ""}))
}

func (a *app) login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	// Constant time, so a wrong guess tells the caller nothing about how wrong it was.
	if subtle.ConstantTimeCompare([]byte(r.PostFormValue("password")), []byte(a.password)) != 1 {
		time.Sleep(500 * time.Millisecond)
		http.Redirect(w, r, "/login?error=1", http.StatusSeeOther)
		return
	}
	id, err := a.newSession()
	if err != nil {
		http.Error(w, "could not start a session", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		Secure:   true, // the tunnel is HTTPS only
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL / time.Second),
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *app) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.mu.Lock()
		delete(a.sessions, c.Value)
		a.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// data is what every page is rendered with, so the title set on the command line reaches all of them.
func (a *app) data(extra map[string]any) map[string]any {
	d := map[string]any{"Title": a.title}
	for k, v := range extra {
		d[k] = v
	}
	return d
}

func (a *app) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.pages.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// --- files ---

type fileInfo struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
}

func (a *app) listFiles(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(a.root)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	files := []fileInfo{}
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileInfo{
			Name:     e.Name(),
			Size:     info.Size(),
			Modified: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Modified > files[j].Modified })
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

func (a *app) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, a.maxBytes)
	reader, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expected a multipart upload"})
		return
	}

	saved := []string{}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if part.FormName() != "files" || part.FileName() == "" {
			part.Close()
			continue
		}
		name, err := a.safePath(part.FileName())
		if err != nil {
			part.Close()
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		// Write beside the target and rename, so a failed upload never leaves a half file in the listing.
		tmp, err := os.CreateTemp(a.root, ".upload-*")
		if err != nil {
			part.Close()
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		_, copyErr := io.Copy(tmp, part)
		part.Close()
		tmp.Close()
		if copyErr != nil {
			os.Remove(tmp.Name())
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": copyErr.Error()})
			return
		}
		if err := os.Rename(tmp.Name(), name); err != nil {
			os.Remove(tmp.Name())
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		saved = append(saved, filepath.Base(name))
	}
	if len(saved) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no files in the request"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": saved})
}

func (a *app) download(w http.ResponseWriter, r *http.Request) {
	name, err := a.safePath(r.PathValue("name"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f, err := os.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	ctype, err := contentType(f, info.Name())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Anything the browser can safely render is offered inline, so an image or a log file opens in
	// a tab instead of landing in the downloads folder. ?download=1 forces the save either way.
	disposition := "attachment"
	if inlineSafe(ctype) && r.URL.Query().Get("download") == "" {
		disposition = "inline"
	}

	w.Header().Set("Content-Type", ctype)
	// The type below was sniffed from content the admin uploaded, so tell the browser to take it
	// literally rather than guessing something more dangerous of its own.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", disposition+"; filename*=UTF-8''"+url.PathEscape(info.Name()))
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

// contentType decides what a file really is: the extension first, because it carries distinctions
// sniffing cannot make (.css and .csv are both plain text on the wire), then a look at the opening
// bytes for the files that arrive without a useful name. The read is undone before returning, so
// the caller still holds a file positioned at the start.
func contentType(f *os.File, name string) (string, error) {
	ext := strings.ToLower(filepath.Ext(name))
	if known, ok := extTypes[ext]; ok {
		return known, nil
	}
	if byExt := mime.TypeByExtension(ext); byExt != "" {
		return normalizeText(byExt), nil
	}

	// http.DetectContentType wants at most 512 bytes and treats a short read as the whole file.
	head := make([]byte, 512)
	n, err := f.Read(head)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return normalizeText(http.DetectContentType(head[:n])), nil
}

// normalizeText gives text types an explicit UTF-8 charset, which is what browsers need before they
// will render one inline without mangling anything outside ASCII.
func normalizeText(ctype string) string {
	base, params, err := mime.ParseMediaType(ctype)
	if err != nil {
		return ctype
	}
	if strings.HasPrefix(base, "text/") && params["charset"] == "" {
		return mime.FormatMediaType(base, map[string]string{"charset": "utf-8"})
	}
	return ctype
}

// inlineSafe reports whether a type is one the browser can render without the page being able to act
// as this site. HTML and SVG are deliberately left out: both carry script, and a tunnel serves every
// file from the same origin as the file list, so rendering one inline would hand it the session.
func inlineSafe(ctype string) bool {
	base, _, err := mime.ParseMediaType(ctype)
	if err != nil {
		return false
	}
	switch base {
	case "text/html", "text/xml", "application/xml", "image/svg+xml", "application/xhtml+xml":
		return false
	case "application/pdf", "application/json":
		return true
	}
	switch {
	case strings.HasPrefix(base, "text/"),
		strings.HasPrefix(base, "image/"),
		strings.HasPrefix(base, "audio/"),
		strings.HasPrefix(base, "video/"):
		return true
	}
	return false
}

func (a *app) deleteFile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": `expected {"name": "..."}`})
		return
	}
	name, err := a.safePath(body.Name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := os.Remove(name); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such file"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": filepath.Base(name)})
}

// safePath turns a client supplied name into a path directly inside the drop folder, or an error.
// Only the base name survives, so ../ and absolute paths cannot escape, and dot files stay out of
// the way of the temporary files uploads are written to.
func (a *app) safePath(name string) (string, error) {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if name == "." || name == ".." || name == "/" || name == "" || strings.HasPrefix(name, ".") {
		return "", errors.New("bad file name")
	}
	return filepath.Join(a.root, name), nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
