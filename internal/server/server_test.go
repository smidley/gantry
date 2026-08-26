package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// freePort allocates and immediately releases a TCP port for
// ListenAndServe (unlike the rest of this package's tests, which go
// through httptest.NewServer and never bind a real, predictable port).
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

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

// TestListenAndServeDrainsLiveClientsOnShutdown pins the carried Batch C
// fix: net/http's Shutdown never force-closes a StateActive connection --
// it only closes idle ones and waits (up to its ctx deadline) for active
// ones to finish on their own. handleLive's stream loop only returns when
// the CLIENT disconnects (ctx.Done() on the *request*, not the server),
// so before this fix, one connected /api/live client made every graceful
// shutdown burn the whole 5s Shutdown budget and come back
// context.DeadlineExceeded (which main.go's caller treats as a fatal
// error, exit 1). The fix: Broadcaster.Drain() closes a shared channel
// handleLive's select also watches, and ListenAndServe calls Drain()
// right after ctx fires but BEFORE hs.Shutdown -- every live handler gets
// nudged to return well within the budget, freeing its connection to go
// idle (and get closed by Shutdown's own poll loop) in well under a
// second.
func TestListenAndServeDrainsLiveClientsOnShutdown(t *testing.T) {
	port := freePort(t)
	live := NewBroadcaster()
	s := New(Options{
		Port:    port,
		Version: "test-1",
		Started: time.Now(),
		Live:    live,
		Current: func() []byte { return []byte(`{}`) },
	})

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- s.ListenAndServe(ctx) }()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	require.Eventually(t, func() bool {
		resp, err := http.Get(base + "/api/healthz")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 2*time.Second, 20*time.Millisecond, "server never came up")

	// Open an SSE client and deliberately leave it connected (unlike every
	// other test in this file/package, which closes its client before
	// asserting on shutdown) -- an open, actively-streaming connection is
	// exactly the StateActive scenario the bug needs.
	c := openSSE(t, base+"/api/live")
	defer c.close()
	c.readFrame(t) // block until Register() succeeded and the connect frame arrived

	start := time.Now()
	cancel()

	select {
	case err := <-runDone:
		elapsed := time.Since(start)
		require.NoError(t, err, "ListenAndServe must return nil, not a Shutdown timeout, with a live client still connected")
		require.Less(t, elapsed, time.Second, "shutdown must drain the live client instead of waiting out the 5s Shutdown budget")
	case <-time.After(6 * time.Second):
		t.Fatal("ListenAndServe never returned")
	}
}

// TestListenAndServeDrainsFollowLogClientsOnShutdown pins the batch D
// review's Finding 1: a follow=1 /api/containers/{name}/logs stream
// blocks on its own Logs closure exactly the way a connected /api/live
// client blocks on its Broadcaster channel -- handleLogs' read loop
// only returns when the underlying reader ends, which for a live
// follow stream (real docker.Collector.StreamLogs's io.Pipe, bound to
// the request ctx) only happens on client disconnect or server
// shutdown. Before this fix, one connected follow=1 client reproduced
// the exact pre-carried-fix SSE bug: Shutdown waits out its whole 5s
// budget for the still-active connection and returns
// context.DeadlineExceeded, which main.go's caller treats as fatal.
// The fix generalizes the drain signal: Server gets its own drain chan
// (closed by ListenAndServe alongside Live.Drain(), before hs.Shutdown)
// that handleLogs' watcher goroutine also selects on, closing the
// Logs ReadCloser to unblock the read loop the same way client
// disconnect already does.
func TestListenAndServeDrainsFollowLogClientsOnShutdown(t *testing.T) {
	port := freePort(t)

	// pr/pw stands in for StreamLogs' real io.Pipe: a follow=1 stream
	// with nothing new to log yet blocks exactly like this -- reading
	// from pr never returns until either the client disconnects or
	// something closes pr out from under it.
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	logsFn := func(context.Context, string, bool, int) (io.ReadCloser, error) {
		return pr, nil
	}
	s := New(Options{Port: port, Version: "test-1", Started: time.Now(), Logs: logsFn})

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- s.ListenAndServe(ctx) }()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	require.Eventually(t, func() bool {
		resp, err := http.Get(base + "/api/healthz")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 2*time.Second, 20*time.Millisecond, "server never came up")

	// Open a follow=1 logs client and deliberately leave it connected,
	// same as the SSE test above: an open connection whose Logs
	// closure never itself returns (pw is never written to or closed
	// by this test) is exactly the StateActive scenario the bug needs.
	logsClient := &http.Client{Timeout: 10 * time.Second}
	logsResp, err := logsClient.Get(base + "/api/containers/anything/logs?follow=1")
	require.NoError(t, err)
	defer func() { _ = logsResp.Body.Close() }()
	require.Equal(t, http.StatusOK, logsResp.StatusCode, "headers must arrive even though no log line has been written yet")

	start := time.Now()
	cancel()

	select {
	case err := <-runDone:
		elapsed := time.Since(start)
		require.NoError(t, err, "ListenAndServe must return nil, not a Shutdown timeout, with a follow=1 logs client still connected")
		require.Less(t, elapsed, time.Second, "shutdown must drain the follow logs client instead of waiting out the 5s Shutdown budget")
	case <-time.After(6 * time.Second):
		t.Fatal("ListenAndServe never returned")
	}
}
