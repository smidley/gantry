package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/docker/docker/api/types/container"
)

// statsFromAPI converts one docker stats API response into the same
// cgStats shape readCgroupStats produces, so recordContainerStats runs
// identically regardless of source. Throttling counters (nr_throttled /
// throttled_usec) aren't mapped here — the API fallback path reports
// cpu.pct/mem/pids/io but not cpu.throttled_pct; that's a fast-path-only
// signal for this phase.
func statsFromAPI(resp container.StatsResponse) cgStats {
	cg := cgStats{
		CPUUsageUsec:    resp.CPUStats.CPUUsage.TotalUsage / 1000, // ns -> usec
		MemCurrent:      resp.MemoryStats.Usage,
		MemInactiveFile: resp.MemoryStats.Stats["inactive_file"],
		Pids:            resp.PidsStats.Current,
		IO:              make(map[string]ioCounters),
	}
	for _, e := range resp.BlkioStats.IoServiceBytesRecursive {
		key := strconv.FormatUint(e.Major, 10) + ":" + strconv.FormatUint(e.Minor, 10)
		c := cg.IO[key]
		switch e.Op {
		case "Read":
			c.RBytes = e.Value
		case "Write":
			c.WBytes = e.Value
		default:
			continue // Sync/Async/Total/Discard etc.: not part of this phase's io.stat-equivalent shape
		}
		cg.IO[key] = c
	}
	return cg
}

// statsViaAPI is the per-container fallback used when the cgroup v2 fast
// path can't read a container's cgroup dir (v1 host, masked path): a
// one-shot docker stats API call, mapped into the same cgStats shape via
// statsFromAPI.
func (c *Collector) statsViaAPI(ctx context.Context, id string) (cgStats, error) {
	reader, err := c.cli.ContainerStatsOneShot(ctx, id)
	if err != nil {
		return cgStats{}, fmt.Errorf("docker: stats API for %s: %w", id, err)
	}
	defer reader.Body.Close()

	var resp container.StatsResponse
	if err := json.NewDecoder(reader.Body).Decode(&resp); err != nil {
		return cgStats{}, fmt.Errorf("docker: decode stats API response for %s: %w", id, err)
	}
	return statsFromAPI(resp), nil
}
