package docker

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/smidley/gantry/internal/collect"
	"github.com/smidley/gantry/internal/store"
)

const diskUsageInterval = 5 * time.Minute

// DiskUsageCollector polls the docker daemon's disk-usage endpoint
// (images/containers/volumes bytes, spec §4.1's docker.img visibility) on
// its own slow interval — a separate Collector from Collector itself
// because 5-minute data has no business sharing a 2s tick loop. Name
// "docker-disk", Interval 5m.
//
// It owns its own client rather than sharing Collector's: the registry
// probes/backs off each collector independently, and a second unix-socket
// connection to the same daemon costs nothing.
type DiskUsageCollector struct {
	cli      *client.Client
	sink     store.MetricSink
	sockPath string
}

var _ collect.Collector = (*DiskUsageCollector)(nil)

// NewDiskUsage constructs the docker-disk collector against the same
// socket path the main docker Collector uses.
func NewDiskUsage(sink store.MetricSink, sockPath string) *DiskUsageCollector {
	cli, err := client.NewClientWithOpts(client.WithHost("unix://"+sockPath), client.WithAPIVersionNegotiation())
	if err != nil {
		log.Printf("docker-disk: client init: %v", err)
	}
	return &DiskUsageCollector{cli: cli, sink: sink, sockPath: sockPath}
}

func (c *DiskUsageCollector) Name() string            { return "docker-disk" }
func (c *DiskUsageCollector) Interval() time.Duration { return diskUsageInterval }

// Probe is the same socket ping Collector.Probe does — the two collectors
// talk to the same daemon, so "is it reachable" is identical.
func (c *DiskUsageCollector) Probe(ctx context.Context) collect.Status {
	if c.cli == nil {
		return collect.Status{Available: false, Detail: "docker client: invalid socket path " + c.sockPath}
	}
	if _, err := c.cli.Ping(ctx); err != nil {
		return collect.Status{Available: false, Detail: "mount the docker socket read-only at " + c.sockPath}
	}
	// Settled eagerly for the same reason Collector.Probe does: the SDK's
	// lazy version negotiation is not goroutine-safe against a concurrent
	// reader of the same client's negotiated state.
	c.cli.NegotiateAPIVersion(ctx)
	return collect.Status{Available: true}
}

// Tick polls SDK DiskUsage and records the three totals under a fixed
// "unraid"-kind "docker" entity: this is Unraid-host storage-pressure
// data (same bucket the array/share metrics live in), not per-container
// data — a filling docker.img is the classic Unraid failure spec §4.1
// calls out.
func (c *DiskUsageCollector) Tick(ctx context.Context, now time.Time) error {
	du, err := c.cli.DiskUsage(ctx, types.DiskUsageOptions{})
	if err != nil {
		return fmt.Errorf("docker-disk: disk usage: %w", err)
	}
	imagesBytes, containersBytes, volumesBytes := sumDiskUsage(du)

	ts := now.Unix()
	key := func(metric string) store.SeriesKey {
		return store.SeriesKey{Kind: "unraid", Entity: "docker", Metric: metric}
	}
	c.sink.Record(key("docker.images_bytes"), ts, imagesBytes)
	c.sink.Record(key("docker.containers_bytes"), ts, containersBytes)
	c.sink.Record(key("docker.volumes_bytes"), ts, volumesBytes)
	return nil
}

// sumDiskUsage extracts the three totals from one DiskUsage response.
// imagesBytes is the daemon's own precomputed layer-sharing-aware total
// (du.LayersSize, already a sum across every image); containersBytes and
// volumesBytes are summed here since the API reports those per-item. A
// nil item or nil UsageData contributes 0, never an error — a daemon that
// only partially populated the response (e.g. a volume driver that
// doesn't report usage) shouldn't fail the whole tick.
func sumDiskUsage(du types.DiskUsage) (imagesBytes, containersBytes, volumesBytes float64) {
	imagesBytes = float64(du.LayersSize)
	for _, ct := range du.Containers {
		if ct != nil {
			containersBytes += float64(ct.SizeRw)
		}
	}
	for _, v := range du.Volumes {
		if v != nil && v.UsageData != nil {
			volumesBytes += float64(v.UsageData.Size)
		}
	}
	return imagesBytes, containersBytes, volumesBytes
}
