// Package server hosts Gantry's HTTP surface: the embedded SPA and /api.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
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

	// Query looks up history for one kind+entity's metrics over [from,to)
	// (main wiring points this at store.QuerySeries) for /api/series. Nil
	// in tests that don't wire one — the route then reports an empty JSON
	// array rather than panicking.
	Query func(ctx context.Context, kind, entity string, metrics []string, from, to int64) ([]store.SeriesResult, error)
	// Top looks up one metric's per-entity leaderboard over [from,to) (main
	// wiring points this at store.TopEntities; kind is always "container"
	// for every resource /api/top exposes today) for /api/top's non-"now"
	// windows — the resource->metric(s) mapping, multi-metric summing, and
	// the window="now" short-circuit via Snapshot all live in the handler,
	// keeping this closure a thin, generic passthrough. Nil in tests that
	// don't wire one — see Query.
	Top func(ctx context.Context, kind, metric string, from, to int64, agg string, limit int) ([]TopRow, error)
	// Events looks up historical events (main wiring points this at
	// store.QueryEvents, a straight passthrough) for /api/events. Nil in
	// tests that don't wire one — see Query.
	Events func(f store.EventFilter) ([]store.Event, error)

	// Live fans out SSE frames to connected /api/live clients (main wiring
	// constructs one *Broadcaster and feeds it from a periodic
	// snapshot-publish goroutine). Nil in tests that don't wire one — the
	// route then reports 503: unlike the other optional closures above,
	// there is no meaningful "empty" stream to fall back to.
	Live *Broadcaster
	// Current returns the latest snapshot, pre-marshaled to JSON, for the
	// immediate frame /api/live writes on connect (main wiring points this
	// at buildSnapshot + json.Marshal). Nil in tests that don't wire one —
	// the route then skips straight to streaming, no connect frame.
	Current func() []byte

	// Logs streams one container's demuxed stdout+stderr as plain text
	// (main wiring points this at docker.Collector.StreamLogs) for
	// GET /api/containers/{name}/logs. Nil in tests that don't wire one,
	// and in fake-data mode (no real docker.Collector at all) — the route
	// then answers 404 the same way an unknown container name does,
	// which the fake-mode UI's log viewer relies on for a graceful empty
	// state rather than a hard error.
	Logs func(ctx context.Context, name string, follow bool, tail int) (io.ReadCloser, error)
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
	s.mux.HandleFunc("GET /api/series", s.handleSeries)
	s.mux.HandleFunc("GET /api/top", s.handleTop)
	s.mux.HandleFunc("GET /api/events", s.handleEvents)
	s.mux.HandleFunc("GET /api/live", s.handleLive)
	s.mux.HandleFunc("GET /api/containers/{name}/logs", s.handleLogs)

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

// writeError writes a {"error":"..."} body with the given status code —
// the shared shape for every 4xx/5xx response across the /api surface.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg}) // see writeJSON: write failure means the client's gone
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
		// Drain live SSE clients BEFORE Shutdown: see Broadcaster.Drain for
		// why Shutdown alone can't get them to disconnect on its own.
		if s.opts.Live != nil {
			s.opts.Live.Drain()
		}
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return hs.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
}
