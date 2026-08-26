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
