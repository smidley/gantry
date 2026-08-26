// Package server hosts Gantry's HTTP surface: the embedded SPA and /api.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"time"

	"github.com/smidley/gantry/internal/store"
)

//go:embed webdist
var webFS embed.FS

type Options struct {
	Port    int
	Version string
	Store   *store.Store
	Started time.Time

	// Sources reports collector name -> "ok" | unavailability detail, for
	// healthz. Nil in tests that don't wire a registry — healthz then
	// reports an empty sources map rather than panicking.
	Sources func() map[string]string
	// Snapshot assembles the current live-metrics snapshot (main wiring
	// builds this from store.Live() + the docker/unraid collectors — the
	// server itself stays store-shape-agnostic). Nil in tests that don't
	// wire one — the snapshot route then reports an empty JSON object
	// rather than panicking.
	Snapshot func() SnapshotDTO
	// Containers lists the current fleet (main wiring calls dc.Running()
	// directly) for /api/containers. Nil in tests that don't wire one —
	// the route then reports an empty JSON array rather than panicking.
	Containers func() []ContainerInfo
}

type Server struct {
	opts Options
	mux  *http.ServeMux
}

func New(o Options) *Server {
	s := &Server{opts: o, mux: http.NewServeMux()}

	s.mux.HandleFunc("GET /api/healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]string{"version": s.opts.Version})
	})
	s.mux.HandleFunc("GET /api/live/snapshot", s.handleSnapshot)
	s.mux.HandleFunc("GET /api/containers", s.handleContainers)

	dist, err := fs.Sub(webFS, "webdist")
	if err != nil {
		panic(err) // embedded FS shape is a compile-time property
	}
	s.mux.Handle("GET /", http.FileServerFS(dist))
	return s
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	sources := map[string]string{}
	if s.opts.Sources != nil {
		sources = s.opts.Sources()
	}
	writeJSON(w, map[string]any{
		"status":   "ok",
		"version":  s.opts.Version,
		"uptime_s": int64(time.Since(s.opts.Started).Seconds()),
		"sources":  sources,
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v) // a write failure here means the client already disconnected
}

// ListenAndServe serves until ctx is cancelled, then shuts down gracefully.
func (s *Server) ListenAndServe(ctx context.Context) error {
	hs := &http.Server{
		Addr:              fmt.Sprintf(":%d", s.opts.Port),
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- hs.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return hs.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
}
