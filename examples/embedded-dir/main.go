// Command embedded-dir publishes a folder on a public HTTPS URL from inside your own program.
//
// It is the file serving half of the tunnel client used as a library: no local web server, no second process, just a directory and a tunnel.
// The client answers requests in process, straight from disk, with an LRU cache in front of it.
//
//	go run ./examples/embedded-dir \
//	  -server https://tunnel.example.com \
//	  -key    your-api-key \
//	  -dir    ./public \
//	  -listing
//
// Add -subdomain to ask for a particular name, and -session to reclaim a URL a previous run was given.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/chriswirz/https-tunnel/tunnelclient"
)

func main() {
	var (
		serverURL = flag.String("server", "", "tunnel server, e.g. https://tunnel.example.com")
		apiKey    = flag.String("key", os.Getenv("TUNNEL_API_KEY"), "api key, or set TUNNEL_API_KEY")
		dir       = flag.String("dir", ".", "directory to publish")
		listing   = flag.Bool("listing", false, "list directories that have no index.html")
		cacheMB   = flag.Int("cache-mb", 64, "size of the in memory file cache, in megabytes; 0 disables it")
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

	root, err := filepath.Abs(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "%s is not a directory\n", root)
		os.Exit(1)
	}

	level := slog.LevelWarn
	if *verbose {
		level = slog.LevelInfo
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	// A session id kept on disk is what makes the URL survive a restart.
	// The command reads its own config file for this; a program embedding the client decides for itself where to put it, which is all OnSession is for.
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

		// Dir serves files from disk. Swap it for Handler to serve an http.Handler
		// instead, or TargetURL to proxy to a server already listening locally.
		Dir:              root,
		CacheBytes:       int64(*cacheMB) << 20,
		DirectoryListing: *listing,

		Logger:     logger,
		ClientInfo: "embedded-dir example",
		OnSession: func(id string) error {
			if *sessFile == "" {
				return nil
			}
			return os.WriteFile(*sessFile, []byte(id), 0o600)
		},
		OnConnect: func(t tunnelclient.Tunnel) {
			fmt.Printf("\n  %s  ->  %s\n  session: %s\n\n", t.URL, root, t.Session)
			if !*listing {
				fmt.Println("  directory listing is off, so folders without an index.html return 403")
			}
			fmt.Println("  anyone with this URL can read every file under that folder")
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
