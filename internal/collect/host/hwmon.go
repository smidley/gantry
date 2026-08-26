package host

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/smidley/gantry/internal/collect"
)

type hwmonKind int

const (
	hwmonTemp hwmonKind = iota
	hwmonFan
)

// hwmonReading is one sensor sample. label is already slugged
// (lowercase, spaces to underscores) and ready to drop into a metric name.
type hwmonReading struct {
	kind  hwmonKind
	label string
	value float64 // temp: degrees C; fan: RPM
}

// scanHwmon walks sysRoot+"/class/hwmon/*/" for temp*_input and fan*_input
// files, pairing each with its chip name and (if present) its own
// *_label file. Returns nil if sysRoot's hwmon tree isn't present.
func scanHwmon(sysRoot string) []hwmonReading {
	base := filepath.Join(sysRoot, "class", "hwmon")
	chips, err := os.ReadDir(base)
	if err != nil {
		return nil
	}

	var out []hwmonReading
	for _, chip := range chips {
		if !chip.IsDir() {
			continue
		}
		chipDir := filepath.Join(base, chip.Name())
		chipName := readTrimmed(filepath.Join(chipDir, "name"))
		if chipName == "" {
			chipName = chip.Name()
		}
		files, err := os.ReadDir(chipDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			name := f.Name()
			if !strings.HasSuffix(name, "_input") {
				continue
			}
			key := strings.TrimSuffix(name, "_input")
			switch {
			case strings.HasPrefix(name, "temp"):
				if raw, err := readInt(filepath.Join(chipDir, name)); err == nil {
					out = append(out, hwmonReading{
						kind:  hwmonTemp,
						label: hwmonLabel(chipDir, key, chipName),
						value: float64(raw) / 1000.0,
					})
				}
			case strings.HasPrefix(name, "fan"):
				if raw, err := readInt(filepath.Join(chipDir, name)); err == nil {
					out = append(out, hwmonReading{
						kind:  hwmonFan,
						label: hwmonLabel(chipDir, key, chipName),
						value: float64(raw),
					})
				}
			}
		}
	}
	dedupeLabels(out)
	return out
}

// hwmonLabel builds the slugged "<chip>_<sensor label>", falling back to
// the sensor's own key (e.g. "temp1") when no <key>_label file exists.
func hwmonLabel(chipDir, key, chipName string) string {
	label := readTrimmed(filepath.Join(chipDir, key+"_label"))
	if label == "" {
		label = key
	}
	return collect.SlugSegment(chipName + " " + label)
}

// dedupeLabels gives every reading beyond the first a distinct label when
// its slugged label collides with an earlier reading of the SAME kind
// (temp/fan are separate collision spaces — tickHwmon already prefixes by
// kind before the label reaches a metric name). Two hwmon chip instances
// of the same model (e.g. two NVMe drives, each with a "Composite"
// sensor) otherwise slug to the same label and collapse into one series.
// Collisions are resolved in scan order (os.ReadDir's sorted-by-filename
// listings, both for chips and for each chip's sensor files), so which
// instance is "_2" vs unsuffixed is deterministic.
func dedupeLabels(out []hwmonReading) {
	seen := make(map[hwmonKind]map[string]int, 2)
	for i := range out {
		byLabel, ok := seen[out[i].kind]
		if !ok {
			byLabel = make(map[string]int)
			seen[out[i].kind] = byLabel
		}
		base := out[i].label
		byLabel[base]++
		if n := byLabel[base]; n > 1 {
			out[i].label = fmt.Sprintf("%s_%d", base, n)
		}
	}
}

func readTrimmed(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func readInt(path string) (int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
}
