package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestRunServesHealthzAndShutsDown(t *testing.T) {
	port := freePort(t)
	env := map[string]string{
		"GANTRY_PORT":      fmt.Sprint(port),
		"GANTRY_DB_PATH":   filepath.Join(t.TempDir(), "g.db"),
		"GANTRY_FAKE_DATA": "1",
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, func(k string) string { return env[k] }, "test-ver") }()

	require.Eventually(t, func() bool {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/healthz", port))
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 50*time.Millisecond)

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("run did not shut down")
	}
}

func TestHealthcheckExitPath(t *testing.T) {
	port := freePort(t)
	// Nothing listening → healthcheck must report failure.
	err := healthcheck(func(k string) string {
		if k == "GANTRY_PORT" {
			return fmt.Sprint(port)
		}
		return ""
	})
	require.Error(t, err)
}

func TestRunReleasesGoroutinesOnCtxCancel(t *testing.T) {
	// Test that run() properly releases background goroutines when context is cancelled.
	// This verifies the fix for the shutdown hang bug.
	port := freePort(t)
	env := map[string]string{
		"GANTRY_PORT":      fmt.Sprint(port),
		"GANTRY_DB_PATH":   filepath.Join(t.TempDir(), "g.db"),
		"GANTRY_FAKE_DATA": "1",
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, func(k string) string { return env[k] }, "test-ver") }()

	// Wait for server to start.
	require.Eventually(t, func() bool {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/healthz", port))
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 50*time.Millisecond)

	// Cancel the context.
	cancel()

	// Verify run() returns quickly (not hung in wg.Wait() forever).
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("run did not return after context cancel (goroutines not released)")
	}
}
