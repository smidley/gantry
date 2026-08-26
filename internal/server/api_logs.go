package server

import (
	"net/http"
	"strconv"
)

const (
	defaultLogTail = 500
	maxLogTail     = 5000
)

// handleLogs serves GET /api/containers/{name}/logs?follow=1&tail=500,
// streaming one container's demuxed stdout+stderr as plain text.
// Options.Logs is nil in tests that don't wire one, and in fake-data
// mode (no real docker.Collector at all) — unlike every other optional
// closure in this package, there's no meaningful "empty" log stream, so
// a nil Logs answers 404 the same as an unknown container: fake-data
// mode's log viewer relies on exactly this degrade-to-404 behavior for
// its own empty state.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	q := r.URL.Query()

	tail := defaultLogTail
	if v := q.Get("tail"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "bad tail")
			return
		}
		tail = n
	}
	if tail > maxLogTail {
		tail = maxLogTail
	}
	follow := q.Get("follow") == "1"

	if s.opts.Logs == nil {
		writeError(w, http.StatusNotFound, "unknown container "+name)
		return
	}
	rc, err := s.opts.Logs(r.Context(), name, follow, tail)
	if err != nil {
		// Both an unknown name and a currently-unreachable docker daemon
		// land here as 404: the caller can't tell (and for the fake-mode
		// log viewer's graceful empty state, shouldn't need to tell) the
		// two apart from this endpoint alone.
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	defer func() { _ = rc.Close() }()

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	buf := make([]byte, 32*1024)
	for {
		n, rerr := rc.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return // client disconnected
			}
			flusher.Flush()
		}
		if rerr != nil {
			return // stream end (non-follow) or ctx-cancel-induced error (follow, client gone)
		}
	}
}
