//go:build dockertest

// Integration test against a real local docker daemon. Not run by the
// default `go test ./...` (see internal/collect/docker/registry_test.go
// for the pure tests that do run by default); invoke explicitly with
// `go test -tags dockertest ./internal/collect/docker/`. Skips itself if
// no daemon answers a ping, so it's safe in environments without one —
// CI wiring for this tag is Task 15.
package docker

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"
)

const dockertestImage = "alpine:3"

func ensureImage(t *testing.T, ctx context.Context, cli *client.Client, ref string) {
	t.Helper()
	if _, err := cli.ImageInspect(ctx, ref); err == nil {
		return
	}
	rc, err := cli.ImagePull(ctx, ref, image.PullOptions{})
	require.NoError(t, err, "pull %s", ref)
	defer rc.Close()
	_, err = io.Copy(io.Discard, rc)
	require.NoError(t, err)
}

func TestDockerCollectorAgainstRealDaemon(t *testing.T) {
	rawCli, err := client.NewClientWithOpts(client.WithHost("unix:///var/run/docker.sock"), client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := rawCli.Ping(ctx); err != nil {
		t.Skip("no local docker daemon reachable: " + err.Error())
	}

	ensureImage(t, ctx, rawCli, dockertestImage)

	created, err := rawCli.ContainerCreate(ctx,
		&container.Config{Image: dockertestImage, Cmd: []string{"sleep", "30"}},
		&container.HostConfig{AutoRemove: true},
		nil, nil, "gantry-dockertest-t6")
	require.NoError(t, err)
	require.NoError(t, rawCli.ContainerStart(ctx, created.ID, container.StartOptions{}))
	t.Cleanup(func() {
		timeout := 1
		_ = rawCli.ContainerStop(context.Background(), created.ID, container.StopOptions{Timeout: &timeout})
	})

	sink := newFakeSink()
	evSink := &fakeEventSink{}
	dc := New(sink, evSink, func(string, string) {}, "/var/run/docker.sock")

	st := dc.Probe(ctx)
	require.True(t, st.Available, "probe: %s", st.Detail)

	require.NoError(t, dc.Tick(ctx, time.Now()))

	require.Eventually(t, func() bool {
		require.NoError(t, dc.Tick(ctx, time.Now()))
		for _, m := range dc.Running() {
			if m.Name == "gantry-dockertest-t6" {
				return m.Pid > 0
			}
		}
		return false
	}, 20*time.Second, 500*time.Millisecond, "gantry-dockertest-t6 never appeared in Running() with a live pid")

	// This dev box has no /host/sys/fs/cgroup (that path is only real
	// inside the CA container on Linux), so readCgroupStats always fails
	// here and every tick already exercises apistats.go's real
	// ContainerStatsOneShot round trip against the daemon — exactly the
	// fallback path Task 8 adds. mem.bytes/pids are unconditional gauges
	// (no rate warm-up needed), so one more tick is enough to see them.
	require.NoError(t, dc.Tick(ctx, time.Now()))
	memBytes, ok := sink.value("gantry-dockertest-t6", "mem.bytes")
	require.True(t, ok, "mem.bytes must be recorded via the stats API fallback")
	require.Greater(t, memBytes, 0.0)
	pids, ok := sink.value("gantry-dockertest-t6", "pids")
	require.True(t, ok, "pids must be recorded via the stats API fallback")
	require.GreaterOrEqual(t, pids, 1.0)

	timeout := 2
	require.NoError(t, rawCli.ContainerStop(ctx, created.ID, container.StopOptions{Timeout: &timeout}))

	require.Eventually(t, func() bool {
		for _, e := range evSink.snapshot() {
			if e.Kind == "container.die" && e.Entity == "gantry-dockertest-t6" {
				return true
			}
		}
		return false
	}, 20*time.Second, 500*time.Millisecond, "container.die event for gantry-dockertest-t6 never arrived")
}
