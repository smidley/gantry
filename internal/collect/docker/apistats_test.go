package docker

import (
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/require"
)

// statsFromAPI: the docker stats API -> cgStats mapping (Task 8), pinned
// with a hand-built container.StatsResponse literal per the brief (no
// daemon needed — the real ContainerStatsOneShot round trip is exercised
// by the dockertest-tagged integration test).
func TestStatsFromAPIMapsFields(t *testing.T) {
	resp := container.StatsResponse{
		CPUStats: container.CPUStats{
			CPUUsage: container.CPUUsage{TotalUsage: 5_000_000_000}, // ns
		},
		MemoryStats: container.MemoryStats{
			Usage: 209715200,
			Stats: map[string]uint64{"inactive_file": 41943040, "anon": 100},
		},
		PidsStats: container.PidsStats{Current: 7},
		BlkioStats: container.BlkioStats{
			IoServiceBytesRecursive: []container.BlkioStatEntry{
				{Major: 8, Minor: 0, Op: "Read", Value: 1_000_000},
				{Major: 8, Minor: 0, Op: "Write", Value: 2_000_000},
				{Major: 8, Minor: 16, Op: "Read", Value: 500_000},
				{Major: 8, Minor: 16, Op: "Write", Value: 300_000},
				{Major: 8, Minor: 16, Op: "Sync", Value: 999}, // neither Read nor Write: ignored
			},
		},
	}

	cg := statsFromAPI(resp)

	require.Equal(t, uint64(5_000_000), cg.CPUUsageUsec, "ns -> usec")
	require.Equal(t, uint64(209715200), cg.MemCurrent)
	require.Equal(t, uint64(41943040), cg.MemInactiveFile)
	require.Equal(t, uint64(7), cg.Pids)
	require.Equal(t, map[string]ioCounters{
		"8:0":  {RBytes: 1_000_000, WBytes: 2_000_000},
		"8:16": {RBytes: 500_000, WBytes: 300_000},
	}, cg.IO)
}

func TestStatsFromAPIMissingInactiveFileDefaultsToZero(t *testing.T) {
	resp := container.StatsResponse{
		MemoryStats: container.MemoryStats{Usage: 1000, Stats: map[string]uint64{}},
	}
	cg := statsFromAPI(resp)
	require.Equal(t, uint64(0), cg.MemInactiveFile)
}
