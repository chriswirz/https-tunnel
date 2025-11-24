// Command embedded-mcp shows an application exposing itself through an https-tunnel with no second process involved.
//
// It stands in for an MCP server: a plain http.Handler, handed to the tunnel client, which serves public requests in process.
// Nothing listens on a local port unless you ask it to, so this also works on a machine where binding a port is awkward.
//
//	go run ./examples/embedded-mcp \
//	  -server https://tunnel.example.com \
//	  -key    your-api-key \
//	  -subdomain my-mcp
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/chriswirz/https-tunnel/tunnelclient"
)

func main() {
	var (
		serverURL = flag.String("server", "", "tunnel server, e.g. https://tunnel.example.com")
		apiKey    = flag.String("key", os.Getenv("TUNNEL_API_KEY"), "api key, or set TUNNEL_API_KEY")
		subdomain = flag.String("subdomain", "", "optional subdomain to request")
		sessionID = flag.String("session", "", "optional session id to resume, keeping the same url")
		alsoLocal = flag.String("listen", "", "optionally serve the same handler locally too, e.g. 127.0.0.1:8756")
	)
	flag.Parse()

	if *serverURL == "" || *apiKey == "" {
		fmt.Fprintln(os.Stderr, "both -server and -key (or TUNNEL_API_KEY) are required")
		os.Exit(2)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	handler := newMCPHandler()

	// The same handler can still be served locally, which is handy while developing.
	if *alsoLocal != "" {
		srv := &http.Server{Addr: *alsoLocal, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
		go func() {
			logger.Info("also listening locally", "addr", *alsoLocal)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Error("local listener failed", "err", err)
			}
		}()
		defer srv.Close()
	}

	tc, err := tunnelclient.New(tunnelclient.Options{
		APIKey:           *apiKey,
		ServerURL:        *serverURL,
		SessionID:        *sessionID,
		SubdomainRequest: *subdomain,
		Handler:          handler,
		Logger:           logger,
		ClientInfo:       "embedded-mcp example",

		// Persist this and pass it back as SessionID next time to keep the URL.
		OnSession: func(id string) error {
			logger.Info("session issued, save this to keep the url", "session", id)
			return nil
		},
		OnConnect: func(t tunnelclient.Tunnel) {
			logger.Info("tunnel is up", "url", t.URL, "subdomain", t.Subdomain)
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Run blocks, reconnecting on its own, and returns when ctx is cancelled.
	if err := tc.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// newMCPHandler stands in for a real MCP server.
// Anything satisfying http.Handler works here, including the routers the MCP SDKs hand you.
func newMCPHandler() http.Handler {
	var calls atomic.Uint64

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})

	// A single JSON-RPC style endpoint, which is the shape an MCP server exposes.
	mux.HandleFunc("POST /mcp", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string          `json:"method"`
			ID     json.RawMessage `json:"id"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]any{
				"echo":  req.Method,
				"calls": calls.Add(1),
			},
		})
	})

	// Streaming works unchanged: response frames are flushed as they are written, so server-sent events reach the caller as they happen.
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		for i := range 10 {
			select {
			case <-r.Context().Done():
				return
			default:
			}
			fmt.Fprintf(w, "data: tick %d\n\n", i)
			flusher.Flush()
			time.Sleep(time.Second)
		}
	})

	return mux
}
