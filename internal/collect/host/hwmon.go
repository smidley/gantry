package host

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	return out
}

// hwmonLabel builds the slugged "<chip>_<sensor label>", falling back to
// the sensor's own key (e.g. "temp1") when no <key>_label file exists.
func hwmonLabel(chipDir, key, chipName string) string {
	label := readTrimmed(filepath.Join(chipDir, key+"_label"))
	if label == "" {
		label = key
	}
	return slug(chipName + " " + label)
}

func slug(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), " ", "_")
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
