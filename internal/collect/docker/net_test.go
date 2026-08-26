package docker

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/smidley/gantry/internal/collect"
	"github.com/stretchr/testify/require"
)

func TestReadContainerNetDevSumsNonLoInterfaces(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "1234", "net"), 0o755))
	data, err := os.ReadFile(filepath.Join("testdata", "net_dev.txt"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "1234", "net", "dev"), data, 0o644))

	rx, tx, err := readContainerNetDev(dir, 1234)
	require.NoError(t, err)
	require.Equal(t, uint64(7000000), rx, "lo must be excluded from the sum")
	require.Equal(t, uint64(1500000), tx)
}

func TestReadContainerNetDevMissingPidErrors(t *testing.T) {
	_, _, err := readContainerNetDev(t.TempDir(), 9999)
	require.Error(t, err)
}

func writeNetDev(t *testing.T, procRoot string, pid int, rx, tx uint64) {
	t.Helper()
	dir := filepath.Join(procRoot, strconv.Itoa(pid), "net")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	content := "Inter-|   Receive                                                |  Transmit\n" +
		" face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n" +
		"    lo:       0       0    0    0    0     0          0         0        0        0    0    0    0     0       0          0\n" +
		"  eth0: " + strconv.FormatUint(rx, 10) + "       0    0    0    0     0          0         0 " + strconv.FormatUint(tx, 10) + "        0    0    0    0     0       0          0\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dev"), []byte(content), 0o644))
}

func TestTickNetSkipsHostNetAndMissingPidButRecordsEligibleContainer(t *testing.T) {
	procRoot := t.TempDir()
	sink := newFakeSink()
	c := &Collector{
		sink:     sink,
		rates:    collect.NewRateTracker(),
		reg:      newRegistry(),
		ProcRoot: procRoot,
	}
	c.reg.applyInventory([]Meta{
		{ID: "a", Name: "web", State: "running", Pid: 555, HostNet: false},
		{ID: "b", Name: "hostnet", State: "running", Pid: 777, HostNet: true},
		{ID: "c", Name: "nopid", State: "running", Pid: 0, HostNet: false},
	}, &fakeEventSink{}, func(string, string) {})

	writeNetDev(t, procRoot, 555, 1_000_000, 2_000_000)
	c.tickNet(time.Unix(1000, 0))

	_, ok := sink.value("web", "net.rx_bps")
	require.False(t, ok, "first tick must not emit a rate")

	writeNetDev(t, procRoot, 555, 1_200_000, 2_400_000) // +200,000 / +400,000 over 2s
	c.tickNet(time.Unix(1002, 0))

	rxBps, ok := sink.value("web", "net.rx_bps")
	require.True(t, ok)
	require.InDelta(t, 100_000.0, rxBps, 1e-9)
	txBps, ok := sink.value("web", "net.tx_bps")
	require.True(t, ok)
	require.InDelta(t, 200_000.0, txBps, 1e-9)

	_, ok = sink.value("hostnet", "net.rx_bps")
	require.False(t, ok, "HostNet containers must be excluded from per-container net attribution")
	_, ok = sink.value("nopid", "net.rx_bps")
	require.False(t, ok, "a container with no known pid yet must be skipped")
}
