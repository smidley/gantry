//go:build dockertest

// Integration test against a real local docker daemon — see
// docker_test.go for the tag's rationale and invocation.
//
// Deliberately read-only: it exercises Collector.Images (ImageList +
// ContainerList against whatever this real daemon actually has), never
// RemoveImages/PruneImages -- those two ultimately call ImageRemove
// against a daemon this test doesn't control and can't safely scope a
// deletion on. Their own interesting logic (partial failure,
// enrichment, aggregation, classification-driven filtering) is already
// fully covered without a daemon at all -- see removeImagesWith/
// pruneImagesWith's own tests in images_test.go.
package docker

import (
	"context"
	"testing"
	"time"

	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"
)

func TestCollectorImagesAgainstRealDaemon(t *testing.T) {
	rawCli, err := client.NewClientWithOpts(client.WithHost("unix:///var/run/docker.sock"), client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := rawCli.Ping(ctx); err != nil {
		t.Skip("no local docker daemon reachable: " + err.Error())
	}

	dc := New(nil, nil, nil, "/var/run/docker.sock")
	st := dc.Probe(ctx)
	require.True(t, st.Available, "probe: %s", st.Detail)

	report, err := dc.Images(ctx)
	require.NoError(t, err)

	validStates := map[string]bool{"in-use": true, "unused": true, "dangling": true}
	for _, im := range report.Images {
		require.NotEmpty(t, im.ID)
		require.True(t, validStates[im.State], "unexpected state %q for image %s", im.State, im.ID)
		require.GreaterOrEqual(t, im.SizeBytes, int64(0))
		if im.State == "in-use" {
			require.NotEmpty(t, im.Containers)
		}
	}
	require.Equal(t, len(report.Images), report.Summary.InUse+report.Summary.Unused+report.Summary.Dangling)
}
