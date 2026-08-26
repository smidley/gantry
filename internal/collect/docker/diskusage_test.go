package docker

import (
	"context"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/volume"
	"github.com/stretchr/testify/require"
)

func TestDiskUsageNameAndInterval(t *testing.T) {
	c := NewDiskUsage(newFakeSink(), "/var/run/docker.sock")
	require.Equal(t, "docker-disk", c.Name())
	require.Equal(t, diskUsageInterval, c.Interval())
}

func TestDiskUsageProbeUnavailableOnBadSocketPath(t *testing.T) {
	c := NewDiskUsage(newFakeSink(), "/dev/null/not-a-socket")
	st := c.Probe(context.Background())
	require.False(t, st.Available)
	require.NotEmpty(t, st.Detail)
}

func TestSumDiskUsageAddsPerItemBytesAndUsesDaemonLayersSize(t *testing.T) {
	rw1, rw2 := int64(1000), int64(2000)
	volSize := int64(500)

	du := types.DiskUsage{
		LayersSize: 12345, // daemon-precomputed, deduped across images
		Containers: []*container.Summary{
			{SizeRw: rw1},
			{SizeRw: rw2},
		},
		Volumes: []*volume.Volume{
			{UsageData: &volume.UsageData{Size: volSize}},
		},
	}

	imagesBytes, containersBytes, volumesBytes := sumDiskUsage(du)
	require.Equal(t, 12345.0, imagesBytes)
	require.Equal(t, 3000.0, containersBytes)
	require.Equal(t, 500.0, volumesBytes)
}

func TestSumDiskUsageToleratesNilItemsAndMissingUsageData(t *testing.T) {
	du := types.DiskUsage{
		LayersSize: 0,
		Containers: []*container.Summary{nil, {SizeRw: 42}},
		Volumes: []*volume.Volume{
			nil,
			{UsageData: nil}, // driver that doesn't report usage
			{UsageData: &volume.UsageData{Size: 7}},
		},
	}

	imagesBytes, containersBytes, volumesBytes := sumDiskUsage(du)
	require.Equal(t, 0.0, imagesBytes)
	require.Equal(t, 42.0, containersBytes)
	require.Equal(t, 7.0, volumesBytes)
}

func TestSumDiskUsageEmptyResponseIsAllZeroNotError(t *testing.T) {
	imagesBytes, containersBytes, volumesBytes := sumDiskUsage(types.DiskUsage{})
	require.Equal(t, 0.0, imagesBytes)
	require.Equal(t, 0.0, containersBytes)
	require.Equal(t, 0.0, volumesBytes)
}
