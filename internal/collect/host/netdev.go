package host

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// IfCounters is one interface's rx/tx byte counters from /proc/net/dev.
// Exported so the docker collector's per-container net reader (net.go,
// which reads the same file shape from inside a container's netns) can
// use ParseNetDev directly instead of duplicating the parser.
type IfCounters struct {
	RxBytes, TxBytes uint64
}

var dropIfacePrefixes = []string{"veth", "virbr", "br-", "tap"}

var dropIfaceExact = map[string]bool{
	"lo":      true,
	"docker0": true,
}

// ParseNetDev reads every interface's rx/tx byte counters from
// /proc/net/dev: two header lines, then "<iface>: <16 counters>" per line.
// Rx bytes is the first counter, tx bytes the ninth.
func ParseNetDev(r io.Reader) (map[string]IfCounters, error) {
	out := make(map[string]IfCounters)
	sc := bufio.NewScanner(r)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		if lineNo <= 2 {
			continue
		}
		line := sc.Text()
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		name := strings.TrimSpace(line[:idx])
		fields := strings.Fields(line[idx+1:])
		if len(fields) < 9 {
			return nil, fmt.Errorf("net/dev: short counters for %q", name)
		}
		rx, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("net/dev: parse rx bytes for %q: %w", name, err)
		}
		tx, err := strconv.ParseUint(fields[8], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("net/dev: parse tx bytes for %q: %w", name, err)
		}
		out[name] = IfCounters{RxBytes: rx, TxBytes: tx}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// filteredIfaces drops loopback and virtual/container networking
// interfaces that aren't meaningful at the host level.
func filteredIfaces(all map[string]IfCounters) map[string]IfCounters {
	out := make(map[string]IfCounters, len(all))
	for name, c := range all {
		if dropIfaceExact[name] {
			continue
		}
		drop := false
		for _, p := range dropIfacePrefixes {
			if strings.HasPrefix(name, p) {
				drop = true
				break
			}
		}
		if !drop {
			out[name] = c
		}
	}
	return out
}
