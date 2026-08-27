//go:build dockertest

// Integration test against a real local docker daemon — see docker_test.go
// for the tag's rationale and invocation.
package docker

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/smidley/gantry/internal/server"
	"github.com/stretchr/testify/require"
)

// TestContainerLogsEndpointAgainstRealDaemon exercises Task 9's full
// path -- real daemon, real *Collector, wired through server.Options.Logs
// exactly the way main.go wires it -- over real HTTP, not just
// StreamLogs called directly.
func TestContainerLogsEndpointAgainstRealDaemon(t *testing.T) {
	rawCli, err := client.NewClientWithOpts(client.WithHost("unix:///var/run/docker.sock"), client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := rawCli.Ping(ctx); err != nil {
		t.Skip("no local docker daemon reachable: " + err.Error())
	}

	ensureImage(t, ctx, rawCli, dockertestImage)

	const name = "gantry-dockertest-logs"
	created, err := rawCli.ContainerCreate(ctx,
		&container.Config{Image: dockertestImage, Cmd: []string{"sh", "-c", "echo line1; echo line2; echo line3; sleep 30"}},
		&container.HostConfig{AutoRemove: true},
		nil, nil, name)
	require.NoError(t, err)
	require.NoError(t, rawCli.ContainerStart(ctx, created.ID, container.StartOptions{}))
	t.Cleanup(func() {
		timeout := 1
		_ = rawCli.ContainerStop(context.Background(), created.ID, container.StopOptions{Timeout: &timeout})
	})

	sink := newFakeSink()
	dc := New(sink, &fakeEventSink{}, func(string, string) {}, "/var/run/docker.sock")
	t.Cleanup(dc.Drain)

	st := dc.Probe(ctx)
	require.True(t, st.Available, "probe: %s", st.Detail)

	require.Eventually(t, func() bool {
		require.NoError(t, dc.Tick(ctx, time.Now()))
		_, ok := dc.LookupByName(name)
		return ok
	}, 20*time.Second, 500*time.Millisecond, "%s never appeared in the registry", name)

	srv := server.New(server.Options{Version: "test", Started: time.Now(), Logs: dc.StreamLogs})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/containers/" + name + "/logs?tail=500")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	require.GreaterOrEqual(t, len(lines), 2, "expected at least two lines, got: %q", string(body))
	require.Contains(t, string(body), "line1")
	require.Contains(t, string(body), "line2")
}
