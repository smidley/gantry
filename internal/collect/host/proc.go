// Package host collects CPU, memory, load, network, disk IO, and hwmon
// (temperature/fan) metrics from the /proc and /sys trees mounted
// read-only into the container.
package host

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// cpuTimes holds one line's worth of jiffy counters from /proc/stat.
type cpuTimes struct {
	user, nice, system, idle, iowait, irq, softirq, steal uint64
}

func (c cpuTimes) total() uint64 {
	return c.user + c.nice + c.system + c.idle + c.iowait + c.irq + c.softirq + c.steal
}

// cpuBusyPct returns the percentage of time busy (not idle, not iowait)
// between two /proc/stat samples of the same CPU, or false if the counters
// didn't advance (first sample, or a counter reset).
func cpuBusyPct(prev, cur cpuTimes) (float64, bool) {
	deltaTotal := float64(cur.total()) - float64(prev.total())
	if deltaTotal <= 0 {
		return 0, false
	}
	deltaIdle := float64(cur.idle) - float64(prev.idle)
	deltaIowait := float64(cur.iowait) - float64(prev.iowait)
	return 100 * (1 - (deltaIdle+deltaIowait)/deltaTotal), true
}

func parseCPUFields(fields []string) (cpuTimes, error) {
	if len(fields) < 9 { // label + user/nice/system/idle/iowait/irq/softirq/steal
		return cpuTimes{}, fmt.Errorf("proc/stat: short cpu line %q", strings.Join(fields, " "))
	}
	var v [8]uint64
	for i := range v {
		n, err := strconv.ParseUint(fields[i+1], 10, 64)
		if err != nil {
			return cpuTimes{}, fmt.Errorf("proc/stat: parse %q: %w", fields[i+1], err)
		}
		v[i] = n
	}
	return cpuTimes{user: v[0], nice: v[1], system: v[2], idle: v[3], iowait: v[4], irq: v[5], softirq: v[6], steal: v[7]}, nil
}

// parseProcStat reads the aggregate "cpu" line and every per-core "cpuN"
// line from /proc/stat, in file order (cpu0, cpu1, ...).
func parseProcStat(r io.Reader) (total cpuTimes, perCore []cpuTimes, err error) {
	haveTotal := false
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 || !strings.HasPrefix(fields[0], "cpu") {
			continue
		}
		ct, perr := parseCPUFields(fields)
		if perr != nil {
			return cpuTimes{}, nil, perr
		}
		if fields[0] == "cpu" {
			total, haveTotal = ct, true
			continue
		}
		perCore = append(perCore, ct)
	}
	if err := sc.Err(); err != nil {
		return cpuTimes{}, nil, err
	}
	if !haveTotal {
		return cpuTimes{}, nil, fmt.Errorf("proc/stat: missing aggregate cpu line")
	}
	return total, perCore, nil
}

// parseMeminfo reads MemTotal, MemAvailable, SwapTotal, SwapFree (in kB)
// from /proc/meminfo. Keys not present are left at 0.
func parseMeminfo(r io.Reader) (memTotal, memAvailable, swapTotal, swapFree uint64, err error) {
	dst := map[string]*uint64{
		"MemTotal:":     &memTotal,
		"MemAvailable:": &memAvailable,
		"SwapTotal:":    &swapTotal,
		"SwapFree:":     &swapFree,
	}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		p, ok := dst[fields[0]]
		if !ok {
			continue
		}
		n, perr := strconv.ParseUint(fields[1], 10, 64)
		if perr != nil {
			return 0, 0, 0, 0, fmt.Errorf("meminfo: parse %q: %w", fields[0], perr)
		}
		*p = n
	}
	if err := sc.Err(); err != nil {
		return 0, 0, 0, 0, err
	}
	return memTotal, memAvailable, swapTotal, swapFree, nil
}

func firstFloatField(r io.Reader, what string) (float64, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, fmt.Errorf("%s: empty", what)
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, fmt.Errorf("%s: parse %q: %w", what, fields[0], err)
	}
	return v, nil
}

// parseLoadavg reads the 1-minute load average, the first field of
// /proc/loadavg.
func parseLoadavg(r io.Reader) (load1 float64, err error) {
	return firstFloatField(r, "loadavg")
}

// parseUptime reads the first field of /proc/uptime (seconds since boot).
func parseUptime(r io.Reader) (uptimeSec float64, err error) {
	return firstFloatField(r, "uptime")
}

// parseArcstats reads the ZFS ARC size (bytes) from the "size" row of
// /proc/spl/kstat/zfs/arcstats. ok is false if the row is absent.
func parseArcstats(r io.Reader) (arcSizeBytes uint64, ok bool) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 || fields[0] != "size" {
			continue
		}
		n, err := strconv.ParseUint(fields[2], 10, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}
