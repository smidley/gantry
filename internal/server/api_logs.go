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
	// rc.Close is called from up to two places (this defer, and the
	// drain watcher below) -- every ReadCloser this handler is ever
	// handed (StreamLogs' io.Pipe in production, io.NopCloser or a
	// plain io.Pipe in tests) tolerates a second Close as a harmless
	// no-op, which is all a discarded `_ =` return value needs.
	defer func() { _ = rc.Close() }()

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	// Flush immediately: WriteHeader alone only records the status --
	// net/http doesn't actually put it on the wire until the first
	// Write or an explicit Flush. Without this, a follow=1 client whose
	// container has nothing new to log yet would see no response at
	// all (not even headers) until the first log line arrives, which
	// for a quiet container could be a very long wait.
	flusher.Flush()

	// handlerDone closes the moment this handler returns, for ANY
	// reason (stream end, client gone, or the watcher's own drain-
	// triggered rc.Close() below) -- an explicit, local signal for the
	// watcher goroutine to stop, rather than relying solely on
	// r.Context() eventually going Done() (which net/http does
	// guarantee once ServeHTTP returns, but a second, local channel
	// makes the exit condition self-contained and doesn't depend on
	// inferring that ordering across goroutines).
	handlerDone := make(chan struct{})
	defer close(handlerDone)

	// A follow=1 stream with nothing new to log yet blocks on rc.Read
	// below exactly the way a connected /api/live client blocks on its
	// Broadcaster channel -- with no client disconnect and no server
	// shutdown, that's correct (this is what "follow" means); Task 9's
	// contract already ends it on client disconnect via ctx binding
	// inside StreamLogs itself. What it does NOT already end on is a
	// graceful server shutdown: net/http's Shutdown doesn't cancel an
	// in-flight request's context just because it's waiting for
	// connections to finish, so this handler would otherwise burn
	// Shutdown's whole budget the same way handleLive used to before
	// the carried fix. This watcher closes rc on the server's drain
	// signal, which unblocks rc.Read (an io.Pipe read returns
	// io.ErrClosedPipe once its writer or reader end is closed) the
	// same way a client disconnect already does.
	go func() {
		select {
		case <-s.drain:
			_ = rc.Close()
		case <-r.Context().Done():
		case <-handlerDone:
		}
	}()

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
			return // stream end (non-follow), ctx-cancel (client gone), or drain (server shutting down -- matters most for a still-blocked follow=1 read, but applies to any in-flight request)
		}
	}
}
