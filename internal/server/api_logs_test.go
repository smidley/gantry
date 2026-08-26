package server

import (
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

// logsCall records one Options.Logs invocation's arguments, for
// asserting param parsing/defaulting the same way capturingQuery does
// for /api/series.
type logsCall struct {
	Name   string
	Follow bool
	Tail   int
}

// capturingLogs returns a fake Options.Logs that records every call and
// answers with a reader over body.
func capturingLogs(calls *[]logsCall, body string) func(context.Context, string, bool, int) (io.ReadCloser, error) {
	return func(_ context.Context, name string, follow bool, tail int) (io.ReadCloser, error) {
		*calls = append(*calls, logsCall{Name: name, Follow: follow, Tail: tail})
		return io.NopCloser(strings.NewReader(body)), nil
	}
}

func TestLogsEndpointStreamsBodyWithPlainTextContentType(t *testing.T) {
	var calls []logsCall
	s := New(Options{Version: "test-1", Started: time.Now(), Logs: capturingLogs(&calls, "line1\nline2\n")})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/containers/jellyfin/logs")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "line1\nline2\n", string(body))

	require.Len(t, calls, 1)
	require.Equal(t, "jellyfin", calls[0].Name)
	require.False(t, calls[0].Follow)
	require.Equal(t, defaultLogTail, calls[0].Tail, "missing tail must default to 500")
}

func TestLogsEndpointParsesFollowAndTail(t *testing.T) {
	var calls []logsCall
	s := New(Options{Version: "test-1", Started: time.Now(), Logs: capturingLogs(&calls, "")})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/containers/radarr/logs?follow=1&tail=42")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.Len(t, calls, 1)
	require.Equal(t, "radarr", calls[0].Name)
	require.True(t, calls[0].Follow)
	require.Equal(t, 42, calls[0].Tail)
}

func TestLogsEndpointCapsTailAt5000(t *testing.T) {
	var calls []logsCall
	s := New(Options{Version: "test-1", Started: time.Now(), Logs: capturingLogs(&calls, "")})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/containers/radarr/logs?tail=999999")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.Len(t, calls, 1)
	require.Equal(t, maxLogTail, calls[0].Tail)
}

func TestLogsEndpointBadTailReturns400(t *testing.T) {
	var calls []logsCall
	s := New(Options{Version: "test-1", Started: time.Now(), Logs: capturingLogs(&calls, "")})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	for _, tail := range []string{"nope", "-5", "0"} {
		resp, err := http.Get(ts.URL + "/api/containers/radarr/logs?tail=" + tail)
		require.NoError(t, err)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode, "tail=%s", tail)
		_ = resp.Body.Close()
	}
	require.Empty(t, calls, "a bad tail must never reach Options.Logs")
}

func TestLogsEndpointUnknownContainerReturns404JSON(t *testing.T) {
	logsErr := func(context.Context, string, bool, int) (io.ReadCloser, error) {
		return nil, errNoSuchContainer("ghost")
	}
	s := New(Options{Version: "test-1", Started: time.Now(), Logs: logsErr})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/containers/ghost/logs")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Contains(t, body["error"], "ghost")
}

// TestLogsEndpointNilOptionReturns404 pins the fake-mode contract: with
// no docker.Collector wired at all (Options.Logs left nil), the route
// degrades to 404 -- the same shape an unknown container gets -- rather
// than a 5xx or 503, so the fake-mode log viewer's empty state has just
// one response shape to handle.
func TestLogsEndpointNilOptionReturns404(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()}) // Logs left nil
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/containers/anything/logs")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestLogsEndpointFlushesEachWriteForFollowMode proves the handler
// flushes per write rather than buffering until the handler returns: a
// slow, hand-fed io.Pipe reader lets the test observe the FIRST write on
// the client side before the SECOND write ever happens server-side --
// impossible if the handler weren't flushing incrementally.
func TestLogsEndpointFlushesEachWriteForFollowMode(t *testing.T) {
	pr, pw := io.Pipe()
	logsFn := func(context.Context, string, bool, int) (io.ReadCloser, error) {
		return pr, nil
	}
	s := New(Options{Version: "test-1", Started: time.Now(), Logs: logsFn})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	respCh := make(chan *http.Response, 1)
	go func() {
		resp, err := http.Get(ts.URL + "/api/containers/follow/logs?follow=1")
		require.NoError(t, err)
		respCh <- resp
	}()

	_, err := pw.Write([]byte("first\n"))
	require.NoError(t, err)

	resp := <-respCh
	defer func() { _ = resp.Body.Close() }()

	buf := make([]byte, 6)
	readDone := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(resp.Body, buf)
		readDone <- err
	}()
	select {
	case err := <-readDone:
		require.NoError(t, err)
		require.Equal(t, "first\n", string(buf))
	case <-time.After(3 * time.Second):
		t.Fatal("client never observed the first write -- handler must flush per write, not buffer until it returns")
	}

	_ = pw.Close() // end the stream so the handler (and this test) can finish
}

// errNoSuchContainer mirrors the shape StreamLogs' real "unknown
// container" error takes (a message naming the container), without this
// test file depending on the docker package.
type errNoSuchContainer string

func (e errNoSuchContainer) Error() string { return "unknown container " + string(e) }
