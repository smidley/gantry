//go:build dockertest

// Integration test against a real local docker daemon — see docker_test.go
// for the tag's rationale and invocation. This exercises
// DiskUsageCollector's Tick against the daemon's real /system/df
// response; sumDiskUsage's own math is covered without a daemon by
// diskusage_test.go.
package docker

import (
	"context"
	"testing"
	"time"

	"github.com/docker/docker/client"
	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

func TestDiskUsageCollectorAgainstRealDaemon(t *testing.T) {
	rawCli, err := client.NewClientWithOpts(client.WithHost("unix:///var/run/docker.sock"), client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := rawCli.Ping(ctx); err != nil {
		t.Skip("no local docker daemon reachable: " + err.Error())
	}

	sink := newFakeSink()
	dc := NewDiskUsage(sink, "/var/run/docker.sock")

	st := dc.Probe(ctx)
	require.True(t, st.Available, "probe: %s", st.Detail)

	require.NoError(t, dc.Tick(ctx, time.Now()))

	key := func(metric string) store.SeriesKey {
		return store.SeriesKey{Kind: "unraid", Entity: "docker", Metric: metric}
	}
	_, ok := sink.records[key("docker.images_bytes")]
	require.True(t, ok, "docker.images_bytes must be recorded")
	_, ok = sink.records[key("docker.containers_bytes")]
	require.True(t, ok, "docker.containers_bytes must be recorded")
	_, ok = sink.records[key("docker.volumes_bytes")]
	require.True(t, ok, "docker.volumes_bytes must be recorded")
}
