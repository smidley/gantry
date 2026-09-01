package gpu

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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

// TestNvidiaProbeNotApplicableWhenNoNvidiaHardwareAtAll pins Scott's own
// report ("I don't have an nvidia GPU, so this shouldn't be showing up
// for me"): no nvidia-smi on PATH AND no PCI device on this box reports
// vendor 0x10de must read as NotApplicable, not the old plain-unavailable
// shape -- that's what tells the frontend (SourcesBanner) to stay silent
// instead of showing an actionable-looking hint for hardware that was
// never there to begin with.
func TestNvidiaProbeNotApplicableWhenNoNvidiaHardwareAtAll(t *testing.T) {
	t.Setenv("PATH", t.TempDir())          // empty dir: nvidia-smi can't be found
	t.Setenv("NVIDIA_VISIBLE_DEVICES", "") // nothing requested either
	c := NewNvidia(newFakeSink(), t.TempDir(), func(string) (string, bool) { return "", false })
	c.SysRoot = t.TempDir() // no vendor files at all -- e.g. an Intel-only box
	st := c.Probe(context.Background())
	require.False(t, st.Available)
	require.True(t, st.NotApplicable)
	require.NotEmpty(t, st.Detail)
}

// TestNvidiaProbeUnavailableWhenBinaryMissingButHardwarePresent is the
// OTHER half of the same fixture split (item 5's own "fixture dir
// with/without a 0x10de vendor file" ask): real Nvidia hardware, but no
// working nvidia-smi integration (missing --runtime=nvidia, say) --
// today's existing, actionable hint must stay exactly as it was, and
// NotApplicable must stay false so the banner keeps showing it.
func TestNvidiaProbeUnavailableWhenBinaryMissingButHardwarePresent(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty dir: nvidia-smi can't be found
	c := NewNvidia(newFakeSink(), t.TempDir(), func(string) (string, bool) { return "", false })
	c.SysRoot = t.TempDir()
	writePCIVendorFile(t, c.SysRoot, "0000:01:00.0", "0x10de\n") // a real Nvidia dGPU is present
	st := c.Probe(context.Background())
	require.False(t, st.Available)
	require.False(t, st.NotApplicable)
	require.Contains(t, st.Detail, "nvidia-smi")
}

// TestNvidiaProbeEnvRequestedForcesEnableHint: a container that asked for
// nvidia (NVIDIA_VISIBLE_DEVICES set) but has no nvidia-smi is a fixable
// misconfiguration, not expected-absent hardware -- so it must get the
// actionable enable hint (NotApplicable=false) even when the PCI scan finds
// nothing (e.g. SysRoot isn't mounted).
func TestNvidiaProbeEnvRequestedForcesEnableHint(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no nvidia-smi
	t.Setenv("NVIDIA_VISIBLE_DEVICES", "all")
	c := NewNvidia(newFakeSink(), t.TempDir(), func(string) (string, bool) { return "", false })
	c.SysRoot = t.TempDir() // no vendor files: PCI scan finds nothing
	st := c.Probe(context.Background())
	require.False(t, st.Available)
	require.False(t, st.NotApplicable, "the user asked for nvidia -- surface the hint, don't stay silent")
	require.Contains(t, st.Detail, "nvidia-smi")
}

// TestNvidiaProbeAvailableIgnoresHardwarePresence confirms the hardware
// scan is only ever consulted on the no-nvidia-smi path -- a working
// nvidia-smi is Available=true regardless of what's in SysRoot (in
// particular, without ever even reading it -- an unset/empty SysRoot,
// the zero value a caller that skips wiring it would leave, must not
// matter here).
func TestNvidiaProbeAvailableIgnoresHardwarePresence(t *testing.T) {
	stubOnPath(t, "nvidia-smi")
	c := NewNvidia(newFakeSink(), t.TempDir(), func(string) (string, bool) { return "", false })
	c.SysRoot = ""
	st := c.Probe(context.Background())
	require.True(t, st.Available)
	require.False(t, st.NotApplicable)
}

// TestNvidiaGPUMeta pins the card-title fix's own Nvidia half: known
// outright, no sysfs read involved (contrast the DRM path's own
// GPUMeta, which does need one per pdev).
func TestNvidiaGPUMeta(t *testing.T) {
	c := NewNvidia(newFakeSink(), t.TempDir(), func(string) (string, bool) { return "", false })
	require.Equal(t, map[string]EntityMeta{"nvidia0": {Vendor: "NVIDIA", Driver: "nvidia"}}, c.GPUMeta())
}

// enoentExec returns a fake `run` that fails exactly the way a glibc
// nvidia-smi does in Gantry's scratch image: the fork/exec syscall returns
// ENOENT because the kernel can't find the ELF interpreter (no dynamic
// loader), even though the binary file is right there on PATH.
func enoentExec() func(context.Context, ...string) (string, error) {
	return func(context.Context, ...string) (string, error) {
		return "", &os.PathError{Op: "fork/exec", Path: "/usr/bin/nvidia-smi", Err: syscall.ENOENT}
	}
}

// TestIsExecLoaderFailureClassification pins the loader-vs-not-there
// distinction: an ENOENT/ENOEXEC exec error counts as the loader case ONLY
// when the binary actually exists on disk; a truly-missing binary, a nil
// error, and any other error do not.
func TestIsExecLoaderFailureClassification(t *testing.T) {
	present := filepath.Join(t.TempDir(), "nvidia-smi")
	require.NoError(t, os.WriteFile(present, []byte("x"), 0o755))
	absent := filepath.Join(t.TempDir(), "gone")

	enoent := &os.PathError{Op: "fork/exec", Path: present, Err: syscall.ENOENT}
	enoexec := &os.PathError{Op: "fork/exec", Path: present, Err: syscall.ENOEXEC}

	require.True(t, isExecLoaderFailure(enoent, present), "ENOENT with the binary present is the loader case")
	require.True(t, isExecLoaderFailure(enoexec, present), "ENOEXEC with the binary present is the loader case")
	require.False(t, isExecLoaderFailure(enoent, absent), "ENOENT with no binary on disk is just not-there")
	require.False(t, isExecLoaderFailure(nil, present), "no error is not a failure")
	require.False(t, isExecLoaderFailure(errors.New("some driver error"), present), "an unrelated error is not the loader case")
}

// TestNvidiaRequested pins the NVIDIA_VISIBLE_DEVICES intent signal: a set,
// non-void value means the container asked for nvidia; unset or "void" (the
// runtime's own "no GPUs" sentinel) means it did not.
func TestNvidiaRequested(t *testing.T) {
	require.True(t, nvidiaRequested(func(string) string { return "all" }))
	require.True(t, nvidiaRequested(func(string) string { return "0" }))
	require.True(t, nvidiaRequested(func(string) string { return "GPU-abcd" }))
	require.False(t, nvidiaRequested(func(string) string { return "" }))
	require.False(t, nvidiaRequested(func(string) string { return "void" }))
}

// TestNvidiaProbeUnavailableWhenPresentButUnrunnable is the heart of issue
// #38: nvidia-smi IS on PATH (the runtime injected it) but can't execute in
// the scratch image. Probe must degrade to a clear, actionable, still-visible
// (NotApplicable=false) hint rather than reporting Available and letting Tick
// spam the loader error on every poll.
func TestNvidiaProbeUnavailableWhenPresentButUnrunnable(t *testing.T) {
	stubOnPath(t, "nvidia-smi")
	c := NewNvidia(newFakeSink(), t.TempDir(), func(string) (string, bool) { return "", false })
	c.run = enoentExec()

	st := c.Probe(context.Background())
	require.False(t, st.Available)
	require.False(t, st.NotApplicable, "the user HAS nvidia and wanted it -- this must stay visible, not silent")
	require.Contains(t, st.Detail, "can't run")
	require.Contains(t, st.Detail, "issues/38")
}

// TestNvidiaLoaderDegradeLogsAtMostOnce is the anti-spam guarantee: however
// many times the runner reprobes an un-runnable nvidia-smi, the loader notice
// is logged at most once, and every probe keeps reporting the same
// unavailable status.
func TestNvidiaLoaderDegradeLogsAtMostOnce(t *testing.T) {
	stubOnPath(t, "nvidia-smi")
	c := NewNvidia(newFakeSink(), t.TempDir(), func(string) (string, bool) { return "", false })
	c.run = enoentExec()

	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(os.Stderr)

	for i := 0; i < 25; i++ {
		st := c.Probe(context.Background())
		require.False(t, st.Available)
		require.Contains(t, st.Detail, "can't run")
	}

	require.Equal(t, 1, strings.Count(logBuf.String(), "issues/38"),
		"the loader notice must be logged at most once across many probes, no 15s spam")
}

// TestNvidiaProbeAvailableWhenExecSucceeds guards the genuinely-working case
// (a real Nvidia box, or a future loader-bearing image): if nvidia-smi runs,
// Probe stays Available and nothing is degraded.
func TestNvidiaProbeAvailableWhenExecSucceeds(t *testing.T) {
	stubOnPath(t, "nvidia-smi")
	c := NewNvidia(newFakeSink(), t.TempDir(), func(string) (string, bool) { return "", false })
	c.run = func(context.Context, ...string) (string, error) { return "NVIDIA-SMI 580.00", nil }

	st := c.Probe(context.Background())
	require.True(t, st.Available)
	require.False(t, st.NotApplicable)
}

// TestNvidiaProbeAbsentBinaryNeverExecs confirms the not-enabled path is
// untouched: with no nvidia-smi on PATH, Probe returns the existing
// enable-hint and never even attempts to exec (there's nothing to run).
func TestNvidiaProbeAbsentBinaryNeverExecs(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty: nvidia-smi not found
	c := NewNvidia(newFakeSink(), t.TempDir(), func(string) (string, bool) { return "", false })
	c.SysRoot = t.TempDir()
	writePCIVendorFile(t, c.SysRoot, "0000:01:00.0", "0x10de\n") // hardware present
	c.run = func(context.Context, ...string) (string, error) {
		t.Fatal("run must not be called when nvidia-smi isn't on PATH")
		return "", nil
	}

	st := c.Probe(context.Background())
	require.False(t, st.Available)
	require.False(t, st.NotApplicable)
	require.Contains(t, st.Detail, "nvidia-smi")
	require.NotContains(t, st.Detail, "issues/38", "absent binary is the enable-hint, not the loader message")
}

// TestNvidiaTickRecordsThroughRunSeam proves a successful exec still parses
// and records exactly as before, now routed through the injectable run seam:
// host gauges plus per-container VRAM attribution.
func TestNvidiaTickRecordsThroughRunSeam(t *testing.T) {
	procRoot := t.TempDir()
	writeFile(t, cgroupPath(procRoot, "100"), "0::/docker/"+jellyfinID+"\n")

	sink := newFakeSink()
	c := NewNvidia(sink, procRoot, dockerLookup(jellyfinID, "jellyfin"))
	c.run = func(_ context.Context, args ...string) (string, error) {
		for _, a := range args {
			if strings.Contains(a, "query-compute-apps") {
				return "100, 512", nil
			}
		}
		return "23, 4096", nil
	}

	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))

	util, ok := sink.value("gpu", "nvidia0", "engine.gpu.busy_pct")
	require.True(t, ok)
	require.Equal(t, 23.0, util)
	mem, ok := sink.value("gpu", "nvidia0", "mem.used_mib")
	require.True(t, ok)
	require.Equal(t, 4096.0, mem)
	vram, ok := sink.value("container", "jellyfin", "gpu.nvidia.mem_mib")
	require.True(t, ok)
	require.Equal(t, 512.0, vram)
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
