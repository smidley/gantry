package docker

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStreamLogsUnknownContainerErrorsWithoutTouchingDaemon pins the
// registry-lookup-first contract: an unknown name must fail fast (an
// error naming the container) without ever reaching the SDK client --
// this is what lets the /api/containers/{name}/logs handler map "unknown
// name" and "docker unavailable" to the same 404 shape, and it's the one
// StreamLogs path exercisable without a real daemon.
func TestStreamLogsUnknownContainerErrorsWithoutTouchingDaemon(t *testing.T) {
	dc := New(newFakeSink(), &fakeEventSink{}, func(string, string) {}, "/var/run/docker.sock")

	rc, err := dc.StreamLogs(context.Background(), "no-such-container", false, 500)
	require.Error(t, err)
	require.Nil(t, rc)
	require.Contains(t, err.Error(), "no-such-container")
}
