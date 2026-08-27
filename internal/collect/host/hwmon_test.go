package host

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func buildHwmonTree(t *testing.T) string {
	t.Helper()
	sysRoot := t.TempDir()

	hwmon0 := filepath.Join(sysRoot, "class", "hwmon", "hwmon0")
	require.NoError(t, os.MkdirAll(hwmon0, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(hwmon0, "name"), []byte("coretemp\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(hwmon0, "temp1_input"), []byte("45000\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(hwmon0, "temp1_label"), []byte("Package id 0\n"), 0o644))

	hwmon1 := filepath.Join(sysRoot, "class", "hwmon", "hwmon1")
	require.NoError(t, os.MkdirAll(hwmon1, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(hwmon1, "name"), []byte("nct6779\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(hwmon1, "fan1_input"), []byte("1200\n"), 0o644))

	return sysRoot
}

func findReading(readings []hwmonReading, kind hwmonKind) *hwmonReading {
	for i := range readings {
		if readings[i].kind == kind {
			return &readings[i]
		}
	}
	return nil
}

func TestScanHwmonResolvesLabelledTemp(t *testing.T) {
	sysRoot := buildHwmonTree(t)
	readings := scanHwmon(sysRoot)

	found := findReading(readings, hwmonTemp)
	require.NotNil(t, found)
	require.Equal(t, "coretemp_package_id_0", found.label)
	require.InDelta(t, 45.0, found.value, 1e-9)
}

func TestScanHwmonFallsBackToIndexWhenLabelMissing(t *testing.T) {
	sysRoot := buildHwmonTree(t)
	readings := scanHwmon(sysRoot)

	found := findReading(readings, hwmonFan)
	require.NotNil(t, found)
	require.Equal(t, "nct6779_fan1", found.label)
	require.Equal(t, 1200.0, found.value)
}

func TestScanHwmonMissingSysRootReturnsNil(t *testing.T) {
	readings := scanHwmon(filepath.Join(t.TempDir(), "does-not-exist"))
	require.Nil(t, readings)
}

func TestScanHwmonChipNameFallsBackToDirWhenNameFileMissing(t *testing.T) {
	sysRoot := t.TempDir()
	dir := filepath.Join(sysRoot, "class", "hwmon", "hwmon3")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "temp1_input"), []byte("30000\n"), 0o644))

	readings := scanHwmon(sysRoot)
	require.Len(t, readings, 1)
	require.Equal(t, "hwmon3_temp1", readings[0].label)
}

// Two NVMe drives both surface a temp sensor labelled "Composite" — same
// chip name, same label, genuinely distinct sensors. Without an instance
// disambiguator they'd collapse into one series; scanHwmon must instead
// suffix the second (and any later) occurrence deterministically, by
// hwmon directory order.
func TestScanHwmonDuplicateLabelsGetDeterministicInstanceSuffixes(t *testing.T) {
	sysRoot := t.TempDir()

	first := filepath.Join(sysRoot, "class", "hwmon", "hwmon4")
	require.NoError(t, os.MkdirAll(first, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(first, "name"), []byte("nvme\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(first, "temp1_input"), []byte("35000\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(first, "temp1_label"), []byte("Composite\n"), 0o644))

	second := filepath.Join(sysRoot, "class", "hwmon", "hwmon5")
	require.NoError(t, os.MkdirAll(second, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(second, "name"), []byte("nvme\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(second, "temp1_input"), []byte("38000\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(second, "temp1_label"), []byte("Composite\n"), 0o644))

	readings := scanHwmon(sysRoot)
	require.Len(t, readings, 2)
	require.Equal(t, "nvme_composite", readings[0].label)
	require.InDelta(t, 35.0, readings[0].value, 1e-9)
	require.Equal(t, "nvme_composite_2", readings[1].label)
	require.InDelta(t, 38.0, readings[1].value, 1e-9)
}

// A fan reading sharing a label with a temp reading must not be
// disambiguated against it — the collision space is per-kind, since
// tickHwmon already prefixes by kind ("temp." vs "fan.") before the
// label ever reaches a metric name.
func TestScanHwmonDedupeIsScopedPerKind(t *testing.T) {
	sysRoot := t.TempDir()
	dir := filepath.Join(sysRoot, "class", "hwmon", "hwmon6")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "name"), []byte("chip\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "temp1_input"), []byte("1000\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "temp1_label"), []byte("main\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fan1_input"), []byte("500\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fan1_label"), []byte("main\n"), 0o644))

	readings := scanHwmon(sysRoot)
	require.Len(t, readings, 2)
	temp := findReading(readings, hwmonTemp)
	fan := findReading(readings, hwmonFan)
	require.Equal(t, "chip_main", temp.label)
	require.Equal(t, "chip_main", fan.label, "same label in a different kind must not get an instance suffix")
}
