package host

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// diskCounters holds one whole-device row's monotonic counters, straight
// off the kernel with no unit conversion beyond what the field itself
// documents. extended is false for a pre-4.18-style row of 10-13 fields
// (ancient kernels report throughput but not latency/saturation): host.go
// uses it to decide whether the fields below mean anything, since a plain
// zero value here is indistinguishable from a genuine zero sample.
type diskCounters struct {
	readSectors, writeSectors uint64

	extended bool

	reads, writes        uint64 // IOs completed -- await_ms's denominator
	msReading, msWriting uint64 // ms spent reading/writing -- await_ms's numerator
	inFlight             uint64 // IOs in progress right now -- a gauge, not a delta
	ioTicks              uint64 // ms with >=1 IO in flight -- util_pct's input
	timeInQueue          uint64 // weighted ms spent queued+serviced -- queue_avg's input
}

// wholeDeviceRe matches whole block devices only (sd[a-z]+, nvme<N>n<N>,
// md<N>), excluding their numbered partitions (sda1, nvme0n1p1, md1p1).
var wholeDeviceRe = regexp.MustCompile(`^(sd[a-z]+|nvme\d+n\d+|md\d+)$`)

// parseDiskstats reads /proc/diskstats, keeping whole-device rows only.
// Fields are 1-indexed per the kernel doc: 3=name, 4=reads completed,
// 6=sectors read, 7=ms spent reading, 8=writes completed, 10=sectors
// written, 11=ms spent writing, 12=IOs currently in progress, 13=ms spent
// doing IO (io_ticks), 14=weighted ms spent doing IO (time_in_queue);
// sectors are 512 bytes each. A row with only 10-13 fields still yields
// the two sector counts -- degrade the row, don't drop the device.
func parseDiskstats(r io.Reader) (map[string]diskCounters, error) {
	out := make(map[string]diskCounters)
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 10 {
			continue
		}
		name := fields[2]
		if !wholeDeviceRe.MatchString(name) {
			continue
		}
		readSectors, err := strconv.ParseUint(fields[5], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("diskstats: parse read sectors for %q: %w", name, err)
		}
		writeSectors, err := strconv.ParseUint(fields[9], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("diskstats: parse write sectors for %q: %w", name, err)
		}
		cnt := diskCounters{readSectors: readSectors, writeSectors: writeSectors}

		if len(fields) >= 14 {
			var perr error
			parseAt := func(idx int, label string) uint64 {
				v, err := strconv.ParseUint(fields[idx], 10, 64)
				if err != nil && perr == nil {
					perr = fmt.Errorf("diskstats: parse %s for %q: %w", label, name, err)
				}
				return v
			}
			cnt.reads = parseAt(3, "reads completed")
			cnt.msReading = parseAt(6, "ms reading")
			cnt.writes = parseAt(7, "writes completed")
			cnt.msWriting = parseAt(10, "ms writing")
			cnt.inFlight = parseAt(11, "ios in progress")
			cnt.ioTicks = parseAt(12, "io ticks")
			cnt.timeInQueue = parseAt(13, "time in queue")
			if perr != nil {
				return nil, perr
			}
			cnt.extended = true
		}
		out[name] = cnt
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// buildDeviceMap reads every row's major:minor -> device name from
// /proc/diskstats fields 1-3, partitions included, so DeviceName can
// resolve whatever major:minor a cgroup io.stat row reports.
func buildDeviceMap(r io.Reader) (map[string]string, error) {
	out := make(map[string]string)
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		out[fields[0]+":"+fields[1]] = fields[2]
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
