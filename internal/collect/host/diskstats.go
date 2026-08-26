package host

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

type diskCounters struct {
	readSectors, writeSectors uint64
}

// wholeDeviceRe matches whole block devices only (sd[a-z]+, nvme<N>n<N>,
// md<N>), excluding their numbered partitions (sda1, nvme0n1p1, md1p1).
var wholeDeviceRe = regexp.MustCompile(`^(sd[a-z]+|nvme\d+n\d+|md\d+)$`)

// parseDiskstats reads /proc/diskstats, keeping whole-device rows only.
// Fields are 1-indexed per the kernel doc: 3=name, 6=sectors read,
// 10=sectors written; sectors are 512 bytes each.
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
		out[name] = diskCounters{readSectors: readSectors, writeSectors: writeSectors}
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
