package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHealthz(t *testing.T) {
	s := New(Options{Port: 0, Version: "test-1", Started: time.Now().Add(-90 * time.Second)})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/healthz")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var body struct {
		Status  string          `json:"status"`
		Version string          `json:"version"`
		UptimeS int64           `json:"uptime_s"`
		Sources map[string]bool `json:"sources"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, "ok", body.Status)
	require.Equal(t, "test-1", body.Version)
	require.GreaterOrEqual(t, body.UptimeS, int64(90))
	require.NotNil(t, body.Sources)
}

func TestRootServesPlaceholder(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}
