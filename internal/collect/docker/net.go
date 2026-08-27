package docker

import (
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/smidley/gantry/internal/collect/host"
	"github.com/smidley/gantry/internal/store"
)

// readContainerNetDev sums rx/tx bytes across every non-loopback
// interface inside one container's netns, read via
// procRoot/<pid>/net/dev from the gantry process's own /proc. Because
// the collector runs with --pid=host, that path IS the container's own
// netns view for its init pid — the same trick host.go's tickNet uses
// with pid 1 for the host's own view, just at a different pid.
func readContainerNetDev(procRoot string, pid int) (rxBytes, txBytes uint64, err error) {
	f, err := os.Open(filepath.Join(procRoot, strconv.Itoa(pid), "net", "dev"))
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = f.Close() }()

	all, err := host.ParseNetDev(f)
	if err != nil {
		return 0, 0, err
	}
	for iface, c := range all {
		if iface == "lo" {
			continue
		}
		rxBytes += c.RxBytes
		txBytes += c.TxBytes
	}
	return rxBytes, txBytes, nil
}

// tickNet records net.rx_bps/tx_bps for every running container that has
// its own network namespace. Containers on host networking are skipped
// (spec: labeled "host network", excluded from per-container attribution
// rather than misattributed) and so is any container the registry
// doesn't yet have a live pid for.
func (c *Collector) tickNet(now time.Time) {
	ts := now.Unix()
	for _, m := range c.reg.running() {
		if m.HostNet || m.Pid == 0 {
			continue
		}
		rx, tx, err := readContainerNetDev(c.ProcRoot, m.Pid)
		if err != nil {
			continue
		}
		if bps, ok := c.rates.Rate(m.Name+".net.rx", now, float64(rx)); ok {
			c.sink.Record(store.SeriesKey{Kind: "container", Entity: m.Name, Metric: "net.rx_bps"}, ts, bps)
		}
		if bps, ok := c.rates.Rate(m.Name+".net.tx", now, float64(tx)); ok {
			c.sink.Record(store.SeriesKey{Kind: "container", Entity: m.Name, Metric: "net.tx_bps"}, ts, bps)
		}
	}
}
