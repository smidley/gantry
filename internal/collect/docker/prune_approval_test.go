package docker

import (
	"context"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/stretchr/testify/require"
)

func TestImagePruneNeverAddsNewInventoryToApprovedSet(t *testing.T) {
	fc := &fakeImagesClient{imageListReturn: []image.Summary{{ID: "approved"}, {ID: "newly-dangling"}}}
	c := &Collector{imgCli: fc}
	_, err := c.PruneImages(context.Background(), "dangling", []string{"approved"})
	require.NoError(t, err)
	require.Equal(t, []string{"approved"}, fc.imageRemoveIDs)
	require.False(t, fc.imageRemoveOptions[0].PruneChildren, "ancestors absent from the preview must survive")
}

func TestContainerPruneRevalidatesOnlyApprovedTargets(t *testing.T) {
	fc := &fakeContainersClient{containerListReturn: []container.Summary{
		summaryOfState("approved", "approved", "exited"),
		summaryOfState("newly-stopped", "newly-stopped", "exited"),
		summaryOfState("now-running", "now-running", "running"),
	}}
	c := &Collector{ctrCli: fc}
	_, err := c.PruneContainers(context.Background(), "exited", 0, []string{"approved", "now-running"})
	require.NoError(t, err)
	require.Equal(t, []string{"approved"}, fc.removeIDs)
}
