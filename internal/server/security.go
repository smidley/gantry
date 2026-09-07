package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"time"
)

// decodeSingleJSON rejects a second value and oversized trailing whitespace,
// as well as malformed input. Request bodies are capped before routing.
func decodeSingleJSON(dec *json.Decoder, into any) error {
	if err := dec.Decode(into); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return err
		}
		return errors.New("body must contain exactly one JSON value")
	}
	return nil
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https: http:; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if isAPIPath(path.Clean(r.URL.Path)) {
			w.Header().Set("Cache-Control", "no-store")
			if isMutatingMethod(r.Method) {
				r.Body = http.MaxBytesReader(w, r.Body, mutationMaxRequestBytes)
				_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(10 * time.Second))
				defer func() { _ = http.NewResponseController(w).SetReadDeadline(time.Time{}) }()
			}
		}
		next.ServeHTTP(w, r)
	})
}

// flushStream bounds a slow consumer without placing a lifetime limit on a
// healthy stream. Call before writes as well, since Write itself can block.
func streamWriteDeadline(w http.ResponseWriter) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Second))
}

func flushStream(w http.ResponseWriter) error {
	return http.NewResponseController(w).Flush()
}

// Cancel a blocked socket write as soon as its session or server ends.
// Joining the watcher keeps it from touching a reused ResponseWriter.
func cancelStreamWrites(ctx context.Context, w http.ResponseWriter) func() {
	controller := http.NewResponseController(w)
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
			_ = controller.SetWriteDeadline(time.Now())
		case <-stop:
		}
	}()
	return func() { close(stop); <-done }
}
