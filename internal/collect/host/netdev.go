package host

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type ifCounters struct {
	rxBytes, txBytes uint64
}

var dropIfacePrefixes = []string{"veth", "virbr", "br-", "tap"}

var dropIfaceExact = map[string]bool{
	"lo":      true,
	"docker0": true,
}

// parseNetDev reads every interface's rx/tx byte counters from
// /proc/net/dev: two header lines, then "<iface>: <16 counters>" per line.
// Rx bytes is the first counter, tx bytes the ninth.
func parseNetDev(r io.Reader) (map[string]ifCounters, error) {
	out := make(map[string]ifCounters)
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
		out[name] = ifCounters{rxBytes: rx, txBytes: tx}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// filteredIfaces drops loopback and virtual/container networking
// interfaces that aren't meaningful at the host level.
func filteredIfaces(all map[string]ifCounters) map[string]ifCounters {
	out := make(map[string]ifCounters, len(all))
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
