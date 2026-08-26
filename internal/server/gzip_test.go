package server

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestGzipJSONEndpointCompressedWhenAccepted pins withGzip's happy path:
// a plain JSON route (healthz, standing in for every other gzip-wrapped
// route -- they all go through the same withGzip call) answers with a
// gzip-encoded body, the right headers, and content that decompresses
// back to the same JSON the handler always produced.
func TestGzipJSONEndpointCompressedWhenAccepted(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})

	req := httptest.NewRequest(http.MethodGet, "/api/healthz", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "gzip", rec.Header().Get("Content-Encoding"))
	require.Contains(t, rec.Header().Values("Vary"), "Accept-Encoding")

	gr, err := gzip.NewReader(rec.Body)
	require.NoError(t, err)
	body, err := io.ReadAll(gr)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(body, &got))
	require.Equal(t, "ok", got["status"])
}

// TestGzipJSONEndpointIdentityWhenNotAccepted pins the other half: a
// client that never sends Accept-Encoding: gzip gets a plain, directly
// JSON-decodable body -- no Content-Encoding at all -- even though the
// route is registered through withGzip.
func TestGzipJSONEndpointIdentityWhenNotAccepted(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})

	req := httptest.NewRequest(http.MethodGet, "/api/healthz", nil)
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, rec.Header().Get("Content-Encoding"))
	require.Contains(t, rec.Header().Values("Vary"), "Accept-Encoding")

	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "ok", got["status"])
}

// TestGzipSSENeverCompressed pins the SSE exclusion: /api/live is
// registered WITHOUT withGzip (see New), so even a client that
// explicitly accepts gzip gets a plain, uncompressed event stream --
// buffering SSE into a gzip frame would defeat the entire point of a
// live stream. The request must set Accept-Encoding itself (rather
// than relying on http.Client's default transparent negotiation) so
// this test actually observes what the server sent, not what the
// Transport silently decoded on the way back.
func TestGzipSSENeverCompressed(t *testing.T) {
	live := NewBroadcaster()
	s := New(Options{
		Version: "test-1",
		Started: time.Now(),
		Live:    live,
		Current: func() []byte { return []byte(`{"ts":1}`) },
	})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/live", nil)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "gzip")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Empty(t, resp.Header.Get("Content-Encoding"), "SSE must never be gzip-encoded even when the client accepts it")

	buf := make([]byte, 64)
	n, err := resp.Body.Read(buf)
	require.NoError(t, err)
	require.Contains(t, string(buf[:n]), "event: frame", "body must be plain-text SSE, not a gzip stream")
}

// TestGzipLogsStreamNeverCompressed pins the other exclusion: the
// container logs route is registered WITHOUT withGzip (see New), so it
// too answers identity-encoded even when the client accepts gzip.
func TestGzipLogsStreamNeverCompressed(t *testing.T) {
	s := New(Options{
		Version: "test-1",
		Started: time.Now(),
		Logs: func(context.Context, string, bool, int) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("hello\n")), nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/containers/demo/logs", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, rec.Header().Get("Content-Encoding"), "log stream must never be gzip-encoded even when the client accepts it")
	require.Equal(t, "hello\n", rec.Body.String())
}
