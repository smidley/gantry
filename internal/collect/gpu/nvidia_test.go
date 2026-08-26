package gpu

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// stubOnPath drops an empty, executable file named name into a fresh temp
// dir and points PATH at only that dir for the duration of the test — just
// enough for exec.LookPath to find it. Nothing ever execs the stub (Probe
// only looks it up), so its content doesn't matter.
func stubOnPath(t *testing.T, name string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755))
	require.NoError(t, os.Chmod(path, 0o755))
	t.Setenv("PATH", dir)
}

func TestNvidiaNameAndInterval(t *testing.T) {
	c := NewNvidia(newFakeSink(), t.TempDir(), func(string) (string, bool) { return "", false })
	require.Equal(t, "nvidia", c.Name())
	require.Equal(t, 15*time.Second, c.Interval())
}

func TestNvidiaProbeAvailableWhenBinaryOnPath(t *testing.T) {
	stubOnPath(t, "nvidia-smi")
	c := NewNvidia(newFakeSink(), t.TempDir(), func(string) (string, bool) { return "", false })
	require.True(t, c.Probe(context.Background()).Available)
}

func TestNvidiaProbeUnavailableWhenBinaryMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty dir: nvidia-smi can't be found
	c := NewNvidia(newFakeSink(), t.TempDir(), func(string) (string, bool) { return "", false })
	st := c.Probe(context.Background())
	require.False(t, st.Available)
	require.NotEmpty(t, st.Detail)
}

func TestParseGPUUtilValidLine(t *testing.T) {
	util, mem, ok := parseGPUUtil("23, 4096\n")
	require.True(t, ok)
	require.Equal(t, 23.0, util)
	require.Equal(t, 4096.0, mem)
}

func TestParseGPUUtilToleratesExtraWhitespace(t *testing.T) {
	util, mem, ok := parseGPUUtil("  0 ,   0  \n")
	require.True(t, ok)
	require.Equal(t, 0.0, util)
	require.Equal(t, 0.0, mem)
}

// Multi-GPU hosts print one line per GPU; v1 only handles nvidiaEntity
// ("nvidia0"), so only the first line is read.
func TestParseGPUUtilOnlyReadsFirstLineOfMultiGPUOutput(t *testing.T) {
	util, mem, ok := parseGPUUtil("50, 1000\n75, 2000\n")
	require.True(t, ok)
	require.Equal(t, 50.0, util)
	require.Equal(t, 1000.0, mem)
}

func TestParseGPUUtilEmptyInputNotOK(t *testing.T) {
	_, _, ok := parseGPUUtil("")
	require.False(t, ok)
}

func TestParseGPUUtilMalformedNotOK(t *testing.T) {
	_, _, ok := parseGPUUtil("not-a-number, also-not\n")
	require.False(t, ok)
}

func TestParseComputeAppsMultipleRows(t *testing.T) {
	apps := parseComputeApps("1234, 2048\n5678, 512\n")
	require.Equal(t, []computeApp{{PID: 1234, MemMiB: 2048}, {PID: 5678, MemMiB: 512}}, apps)
}

// nvidia-smi's CSV mode prints nothing at all (not "No running processes
// found", which is only the human-readable mode's text) when no compute
// processes exist.
func TestParseComputeAppsEmptyOutputYieldsNoRows(t *testing.T) {
	apps := parseComputeApps("")
	require.Empty(t, apps)
}

func TestParseComputeAppsSkipsMalformedRowsButKeepsGoodOnes(t *testing.T) {
	apps := parseComputeApps("not-a-pid, 100\n1234, 2048\nmissing-second-field\n")
	require.Equal(t, []computeApp{{PID: 1234, MemMiB: 2048}}, apps)
}

func TestRecordComputeAppsAttributesToContainerAndSkipsUnattributed(t *testing.T) {
	procRoot := t.TempDir()
	writeFile(t, cgroupPath(procRoot, "100"), "0::/docker/"+jellyfinID+"\n") // containerized process
	writeFile(t, cgroupPath(procRoot, "200"), "0::/init.scope\n")            // real host-side process

	sink := newFakeSink()
	c := NewNvidia(sink, procRoot, dockerLookup(jellyfinID, "jellyfin"))
	c.recordComputeApps([]computeApp{
		{PID: 100, MemMiB: 512},
		{PID: 200, MemMiB: 256},
	}, 1000)

	v, ok := sink.value("container", "jellyfin", "gpu.nvidia.mem_mib")
	require.True(t, ok)
	require.Equal(t, 512.0, v)

	require.Len(t, sink.records, 1, "the unattributed host-side process must not produce a container series")
}
