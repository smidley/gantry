package server

import (
	"compress/gzip"
	"net/http"
	"strings"
	"sync"
)

// gzipWriterPool recycles *gzip.Writer values across requests (spec §6:
// "all responses gzip") -- allocating and warming up a fresh compressor
// per request is wasted work at this endpoint's request rates.
var gzipWriterPool = sync.Pool{
	New: func() any { return gzip.NewWriter(nil) },
}

// gzipResponseWriter wraps an http.ResponseWriter so every Write goes
// through gz instead of straight to the client. Header()/WriteHeader
// are inherited unchanged via embedding: headers (including the
// Content-Encoding/Vary this file sets) must land in the shared header
// map before the first Write, exactly like any other handler.
type gzipResponseWriter struct {
	http.ResponseWriter
	gz *gzip.Writer
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	return g.gz.Write(b)
}

// Flush satisfies http.Flusher: flush gz's buffered bytes onward, then
// flush the underlying ResponseWriter. Nothing registered through
// withGzip today is itself a streaming handler that flushes mid-
// response (both streaming routes -- /api/live and the logs tail -- are
// deliberately excluded from withGzip entirely), but implementing this
// correctly rather than silently no-op-ing keeps that exclusion a
// choice about compression semantics, not a missing capability here.
func (g *gzipResponseWriter) Flush() {
	_ = g.gz.Flush() // a flush failure here means the client already disconnected
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// acceptsGzip reports whether the request's Accept-Encoding lists gzip.
// A substring check rather than full content-negotiation parsing
// (quality values, wildcards) -- every real browser and HTTP client
// that supports gzip at all sends a plain "gzip" token.
func acceptsGzip(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept-Encoding"), "gzip")
}

// withGzip wraps next so its response is gzip-compressed whenever the
// request's Accept-Encoding allows it (spec §6 "all responses gzip").
// EXCLUDED from every caller of this function: /api/live (SSE must
// flush each event uncompressed as it's produced -- buffering into a
// gzip frame defeats the entire point of a live stream) and the
// follow=1 logs tail (same reason -- an unbounded stream, not a single
// complete response). Vary: Accept-Encoding is set unconditionally,
// even on the identity path, so a cache sitting in front of this never
// serves a gzip response to a client that didn't ask for one or vice
// versa.
func withGzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding")
		if !acceptsGzip(r) {
			next.ServeHTTP(w, r)
			return
		}

		gz := gzipWriterPool.Get().(*gzip.Writer)
		gz.Reset(w)
		defer func() {
			_ = gz.Close() // a close failure here means the client already disconnected
			gzipWriterPool.Put(gz)
		}()

		w.Header().Set("Content-Encoding", "gzip")
		// The eventual body length no longer matches whatever a handler
		// might have already sized: strip Content-Length rather than
		// serve one that describes the uncompressed body's size.
		w.Header().Del("Content-Length")
		next.ServeHTTP(&gzipResponseWriter{ResponseWriter: w, gz: gz}, r)
	})
}
