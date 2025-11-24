// Command https-tunnel tunnels a local HTTP port to a public HTTPS URL.
//
// One binary plays both parts.
// With a server section in the config it is the public proxy that issues sessions and serves <label>.<base domain>, and it also serves the web frontend embedded below; with a client section it opens a tunnel to such a proxy and forwards traffic to a local port.
// Running both at once is supported and is the easiest way to try the whole thing on one machine.
//
// Usage:
//
//	https-tunnel [-c config.json] [client|server|version]
//
// With no subcommand every enabled section in the config is started.
package main

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chriswirz/https-tunnel/internal/config"
	"github.com/chriswirz/https-tunnel/internal/server"
	"github.com/chriswirz/https-tunnel/tunnelclient"
)

// webDist embeds the statically exported Next.js frontend from web/out.
// That directory is gitignored apart from a .gitkeep, which is what lets this embed resolve, and so the module compile, in a checkout where `npm run build` has never run.
// A real build in ./web fills the directory in around it.
//
//go:embed all:web/out
var webDist embed.FS

// exampleConfig is the annotated configuration shipped with the source, printed by --example-config so a new install can start from a known good file without hunting for the repository.
//
//go:embed config.example.json
var exampleConfig []byte

// appIcon is the application icon, served at /favicon.ico.
// Browsers ask for that path on their own, whatever a page's link tags say, and some clients ask for nothing else, so it is answered from the binary rather than left to the frontend export.
// This is the small three size file written by ./tools/mkicon, not appicon.ico: that one is the Windows executable icon and is twenty times the size.
// The icon.png and apple-icon.png that pages link to come from the same tool and ship inside the export.
//
//go:embed favicon.ico
var appIcon []byte

// version is stamped at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "https-tunnel: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	var (
		configPath  = flag.String("config", defaultConfigPath(), "path to the configuration file, e.g. config1.json")
		configShort = flag.String("c", "", "shorthand for -config")
		logLevel    = flag.String("log-level", "info", "debug, info, warn or error")
		showExample = flag.Bool("example-config", false, "print an example configuration file and exit")
	)
	flag.Usage = usage
	flag.Parse()

	// -c is the shorthand, so whichever one was given wins over the default.
	if *configShort != "" {
		*configPath = *configShort
	}

	if *showExample {
		// Straight to stdout, so it pipes: https-tunnel --example-config > config.json
		_, err := os.Stdout.Write(exampleConfig)
		return err
	}

	mode := "both"
	if args := flag.Args(); len(args) > 0 {
		mode = args[0]
	}
	if mode == "version" {
		fmt.Println("https-tunnel " + version)
		return nil
	}

	logger := newLogger(*logLevel)
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	runClient := cfg.Client.IsEnabled() && (mode == "both" || mode == "client")
	runServer := cfg.Server.IsEnabled() && (mode == "both" || mode == "server")
	switch {
	case mode != "both" && mode != "client" && mode != "server":
		usage()
		return fmt.Errorf("unknown subcommand %q", mode)
	case mode == "client" && !runClient:
		return errors.New("the client section is missing or disabled in " + cfg.Path())
	case mode == "server" && !runServer:
		return errors.New("the server section is missing or disabled in " + cfg.Path())
	case !runClient && !runServer:
		return errors.New("nothing to run: enable the client or server section in " + cfg.Path())
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	errs := make(chan error, 2)

	if runServer {
		srv, err := server.New(cfg.Server, logger.With("role", "server"), frontend(logger), cfg.SaveAdminAccount)
		if err != nil {
			return err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := srv.Run(ctx); err != nil {
				errs <- fmt.Errorf("server: %w", err)
				stop()
			}
		}()
	}

	if runClient {
		cl, err := tunnelclient.New(clientOptions(cfg, logger.With("role", "client")))
		if err != nil {
			return err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := cl.Run(ctx); err != nil {
				errs <- fmt.Errorf("client: %w", err)
				stop()
			}
		}()
	}

	wg.Wait()
	close(errs)
	return <-errs
}

// clientOptions maps the config file's client section onto the library's options.
// The library takes no configuration file of its own, which is what lets another application embed it, so this is the one place the two representations meet.
func clientOptions(cfg *config.Config, logger *slog.Logger) tunnelclient.Options {
	c := cfg.Client
	opts := tunnelclient.Options{
		APIKey:             c.APIKey,
		ServerURL:          c.ServerURL,
		SessionID:          c.SessionID,
		SubdomainRequest:   c.SubdomainRequest,
		InsecureSkipVerify: c.InsecureSkipVerify,
		Logger:             logger,
		OnSession:          cfg.SaveSessionID,
		OnConnect: func(t tunnelclient.Tunnel) {
			// The banner belongs to the command, not to the library: a program embedding the client should not have anything printed to its stdout.
			fmt.Printf("\n  %s  ->  %s\n  session: %s\n\n", t.URL, describeTarget(c), t.Session)
		},
	}
	if c.ServesDirectory() {
		opts.Dir = c.LocalDir
		opts.CacheBytes = int64(c.CacheMB) << 20
		opts.DirectoryListing = c.DirectoryListing
	} else {
		if c.LocalDir != "" {
			// Both are configured, which is allowed; say which one is in use rather than leaving it to be guessed from behavior.
			logger.Info("both local_port and local_dir are set, so the port wins and the directory is ignored",
				"local_port", c.LocalPort, "local_dir", c.LocalDir)
		}
		opts.TargetURL = fmt.Sprintf("%s://%s:%d", c.LocalScheme, c.LocalHost, c.LocalPort)
	}
	return opts
}

// describeTarget names what the client is exposing, for the startup banner.
func describeTarget(c *config.ClientConfig) string {
	if c.ServesDirectory() {
		if c.CacheMB > 0 {
			return fmt.Sprintf("%s (dir, %d MB cache)", c.LocalDir, c.CacheMB)
		}
		return c.LocalDir + " (dir)"
	}
	return fmt.Sprintf("%s://%s:%d", c.LocalScheme, c.LocalHost, c.LocalPort)
}

// webFrontend serves the embedded static export with the fallbacks a client routed application needs.
type webFrontend struct {
	files  fs.FS
	server http.Handler
}

// buildTime stamps the embedded assets for conditional requests.
// The binary's own modification time is the closest thing to a build stamp available at runtime, and a fixed fallback is fine: these bytes only change when the binary does.
var buildTime = func() time.Time {
	if exe, err := os.Executable(); err == nil {
		if info, err := os.Stat(exe); err == nil {
			return info.ModTime()
		}
	}
	return time.Time{}
}()

// frontend prepares the embedded Next.js export for serving.
func frontend(logger *slog.Logger) server.Frontend {
	sub, err := fs.Sub(webDist, "web/out")
	if err != nil {
		logger.Warn("no embedded frontend, serving the api only", "err", err)
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		// Building the backend without building the frontend is a legitimate way to work, so this is a warning rather than a failure.
		logger.Warn("web/out has no index.html, serving the api only", "hint", "run npm run build in ./web")
		return nil
	}
	return &webFrontend{files: sub, server: http.FileServer(http.FS(sub))}
}

// ServePage writes one exported page by name, so the proxy can answer with 404 or offline.
// Any placeholders in vars are replaced first; the export ships them as literal text, and a page served without going through here simply keeps them, which the frontend treats as "no value".
func (f *webFrontend) ServePage(w http.ResponseWriter, _ *http.Request, name string, status int, vars map[string]string) bool {
	body, err := fs.ReadFile(f.files, name+".html")
	if err != nil {
		return false
	}
	if len(vars) > 0 {
		pairs := make([]string, 0, len(vars)*2)
		for from, to := range vars {
			pairs = append(pairs, from, to)
		}
		body = []byte(strings.NewReplacer(pairs...).Replace(string(body)))
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
	return true
}

func (f *webFrontend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	clean := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	switch {
	// Answered here rather than from the export, and only on the control plane host: a tunnel's own /favicon.ico belongs to whatever the client is serving.
	case clean == "favicon.ico":
		w.Header().Set("Content-Type", "image/x-icon")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		http.ServeContent(w, r, "favicon.ico", buildTime, bytes.NewReader(appIcon))
		return
	case clean == "" || clean == "." || clean == "index.html":
		f.ServePage(w, r, "index", http.StatusOK, nil)
		return
	// An exact hit: /_next/static/..., /favicon.ico, /sessions.html.
	case exists(f.files, clean):
		f.server.ServeHTTP(w, r)
		return
	// The export writes /sessions as sessions.html.
	case exists(f.files, clean+".html"):
		r.URL.Path += ".html"
		f.server.ServeHTTP(w, r)
		return
	// /sessions/{id} is one exported placeholder page that reads the id from the address bar.
	case strings.HasPrefix(clean, "sessions/"):
		if f.ServePage(w, r, "sessions/__placeholder__", http.StatusOK, nil) {
			return
		}
	}
	// Reached only on the control plane, where the main site is this host, so the
	// placeholder in the page becomes a plain root link.
	if !f.ServePage(w, r, "404", http.StatusNotFound, map[string]string{"__TUNNEL_BASE_URL__": "/"}) {
		http.NotFound(w, r)
	}
}

func exists(fsys fs.FS, name string) bool {
	if name == "" {
		return false
	}
	info, err := fs.Stat(fsys, name)
	return err == nil && !info.IsDir()
}

func usage() {
	fmt.Fprint(os.Stderr, `https-tunnel tunnels a local HTTP port to a public HTTPS URL.

usage:
  https-tunnel [flags] [command]

examples:
  https-tunnel --example-config > config.json   write a config to start from
  https-tunnel --config config1.json server     run the server from a named file
  https-tunnel -c client.json client            the same, with the shorthand

commands:
  client    run only the client section of the config
  server    run only the server section of the config
  version   print the version
  (none)    run every enabled section

flags:
`)
	flag.PrintDefaults()
}

// defaultConfigPath prefers a config.json next to the binary, falling back to the working directory.
func defaultConfigPath() string {
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "config.json")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return "config.json"
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}
