package docker

import (
	"testing"
	"time"

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
			CPUUsage:       container.CPUUsage{TotalUsage: 5_000_000_000}, // ns
			ThrottlingData: container.ThrottlingData{ThrottledTime: 750_000_000, ThrottledPeriods: 25},
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
	require.Equal(t, uint64(750_000), cg.ThrottledUsec, "ns -> usec")
	require.Equal(t, uint64(25), cg.NrThrottled)
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

// TestStatsFromAPIThroughRecordContainerStatsComputesHostShareCPU pins the
// fallback path's own math end to end: two API-shaped responses 2 seconds
// apart, mapped via statsFromAPI, must feed recordContainerStats' CPU rate
// the same way the cgroup v2 fast path does -- 4,000,000,000ns (4,000,000
// usec) of usage over 2s wall is 2 full cores, 25% of an 8-core host.
func TestStatsFromAPIThroughRecordContainerStatsComputesHostShareCPU(t *testing.T) {
	sink := newFakeSink()
	c := newStatsCollector(sink) // HostCores stubbed to 8

	first := statsFromAPI(container.StatsResponse{})
	second := statsFromAPI(container.StatsResponse{
		CPUStats: container.CPUStats{CPUUsage: container.CPUUsage{TotalUsage: 4_000_000_000}},
	})

	c.recordContainerStats("web", first, time.Unix(1000, 0))
	c.recordContainerStats("web", second, time.Unix(1002, 0))

	cores, ok := sink.value("web", "cpu.cores")
	require.True(t, ok)
	require.Equal(t, 2.0, cores)

	pct, ok := sink.value("web", "cpu.pct")
	require.True(t, ok)
	require.Equal(t, 25.0, pct)
}
