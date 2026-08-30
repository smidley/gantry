package alert

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

func TestNotifyChannelIDIsNotify(t *testing.T) {
	c := NewNotifyChannel(t.TempDir(), nil, nil)
	require.Equal(t, "notify", c.ID())
}

func TestNotifyChannelHealthOKOnWritableDir(t *testing.T) {
	dir := t.TempDir()
	c := NewNotifyChannel(dir, nil, nil)
	require.Equal(t, "ok", c.Health())

	// The probe must clean up after itself: no leftover marker anywhere
	// under dir once Health() returns "ok".
	var leftover []string
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && strings.Contains(info.Name(), "gantry-probe") {
			leftover = append(leftover, path)
		}
		return nil
	})
	require.Empty(t, leftover, "probe marker file was not cleaned up")
}

func TestNotifyChannelHealthReportsVerbatimHintOnReadOnlyDir(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) // let t.TempDir() clean itself up

	c := NewNotifyChannel(dir, nil, nil)
	require.Equal(t, notifyUnavailableHint, c.Health())
}

func TestNotifyChannelHealthProbesAtConstructionNotJustFirstCall(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	c := NewNotifyChannel(dir, nil, nil)
	// Fix the permission AFTER construction: if Health() only probed
	// lazily on first call, it would now report "ok" -- but the plan
	// says construction itself probes, so the cached unhealthy result
	// (not yet 60s stale) must still be what a call right after
	// construction reports.
	require.NoError(t, os.Chmod(dir, 0o700))
	require.Equal(t, notifyUnavailableHint, c.Health())
}

func TestNotifyChannelHealthReprobesAfterInterval(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o500))

	now := time.Unix(1_800_000_000, 0)
	clock := func() time.Time { return now }
	c := NewNotifyChannel(dir, nil, clock)
	require.Equal(t, notifyUnavailableHint, c.Health())

	require.NoError(t, os.Chmod(dir, 0o700))
	now = now.Add(30 * time.Second)
	require.Equal(t, notifyUnavailableHint, c.Health(), "cached result should stand inside the 60s probe interval")

	now = now.Add(31 * time.Second) // total 61s: past the interval
	require.Equal(t, "ok", c.Health())
}

func TestNotifyChannelSendSeverityToImportanceMapping(t *testing.T) {
	cases := []struct {
		severity, wantImportance string
	}{
		{"info", "normal"},
		{"warning", "warning"},
		{"alert", "alert"},
	}
	for _, tc := range cases {
		dir := t.TempDir()
		c := NewNotifyChannel(dir, nil, nil)
		n := AlertNotification{
			Phase:    "fired",
			Rule:     store.AlertRule{ID: "r1", Name: "Rule One", Severity: tc.severity},
			Instance: store.AlertInstance{Entity: "disk3"},
			Summary:  "disk3 is at 57.0 C (over 55.0 C for 10 minutes)",
		}
		res := c.Send(context.Background(), n)
		require.True(t, res.OK, tc.severity)
		require.Equal(t, 1, res.Attempts)

		body := readOneNotifyFile(t, dir)
		require.Contains(t, body, "importance="+tc.wantImportance)
	}
}

func TestNotifyChannelSendSubjectIsRuleNameAndEntity(t *testing.T) {
	dir := t.TempDir()
	c := NewNotifyChannel(dir, nil, nil)
	n := AlertNotification{
		Phase:    "fired",
		Rule:     store.AlertRule{ID: "disk-temp-high", Name: "Disk temperature high", Severity: "warning"},
		Instance: store.AlertInstance{Entity: "disk3"},
		Summary:  "disk3 is at 57.0 C (over 55.0 C for 10 minutes)",
	}
	res := c.Send(context.Background(), n)
	require.True(t, res.OK)

	body := readOneNotifyFile(t, dir)
	require.Contains(t, body, "subject=Disk temperature high — disk3")
	require.Contains(t, body, "description=disk3 is at 57.0 C (over 55.0 C for 10 minutes)")
}

func TestNotifyChannelSendHostEntityFallsBackToHostInSubject(t *testing.T) {
	dir := t.TempDir()
	c := NewNotifyChannel(dir, nil, nil)
	n := AlertNotification{
		Phase:    "fired",
		Rule:     store.AlertRule{ID: "host-cpu-high", Name: "CPU usage high", Severity: "warning"},
		Instance: store.AlertInstance{Entity: ""},
		Summary:  "host is at 90.0 (over 85.0 for 10 minutes)",
	}
	res := c.Send(context.Background(), n)
	require.True(t, res.OK)
	body := readOneNotifyFile(t, dir)
	require.Contains(t, body, "subject=CPU usage high — host")
}

func TestNotifyChannelSendResolvedPrefixesSubjectAndForcesNormalImportance(t *testing.T) {
	dir := t.TempDir()
	c := NewNotifyChannel(dir, nil, nil)
	n := AlertNotification{
		Phase:    "resolved",
		Rule:     store.AlertRule{ID: "disk-temp-high", Name: "Disk temperature high", Severity: "alert"},
		Instance: store.AlertInstance{Entity: "disk3"},
		Summary:  "disk3 recovered",
	}
	res := c.Send(context.Background(), n)
	require.True(t, res.OK)
	body := readOneNotifyFile(t, dir)
	require.Contains(t, body, "subject=Resolved: Disk temperature high — disk3")
	require.Contains(t, body, "importance=normal")
}

func TestNotifyChannelSendSubjectOverrideBypassesRuleEntityConstruction(t *testing.T) {
	dir := t.TempDir()
	c := NewNotifyChannel(dir, nil, nil)
	n := AlertNotification{
		Phase:   "throttled",
		Subject: "3 Gantry alerts suppressed",
		Rule:    store.AlertRule{Severity: "warning"},
		Summary: "disk-temp-high/disk3, host-cpu-high/host",
	}
	res := c.Send(context.Background(), n)
	require.True(t, res.OK)
	body := readOneNotifyFile(t, dir)
	require.Contains(t, body, "subject=3 Gantry alerts suppressed")
	require.Contains(t, body, "description=disk-temp-high/disk3, host-cpu-high/host")
}

func TestNotifyChannelSendLinkOmittedWhenLinkBaseEmpty(t *testing.T) {
	dir := t.TempDir()
	c := NewNotifyChannel(dir, func() string { return "" }, nil)
	n := AlertNotification{Phase: "fired", Rule: store.AlertRule{Name: "R", Severity: "warning"}, Instance: store.AlertInstance{Entity: "e"}}
	res := c.Send(context.Background(), n)
	require.True(t, res.OK)
	body := readOneNotifyFile(t, dir)
	require.NotContains(t, body, "link=")
}

func TestNotifyChannelSendLinkPresentWhenLinkBaseSet(t *testing.T) {
	dir := t.TempDir()
	c := NewNotifyChannel(dir, func() string { return "http://gantry.local:8380" }, nil)
	n := AlertNotification{Phase: "fired", Rule: store.AlertRule{Name: "R", Severity: "warning"}, Instance: store.AlertInstance{Entity: "e"}}
	res := c.Send(context.Background(), n)
	require.True(t, res.OK)
	body := readOneNotifyFile(t, dir)
	require.Contains(t, body, "link=http://gantry.local:8380")
}

func TestNotifyChannelSendFailsWhenDirUnwritable(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	c := NewNotifyChannel(dir, nil, nil)
	n := AlertNotification{Phase: "fired", Rule: store.AlertRule{Name: "R", Severity: "warning"}, Instance: store.AlertInstance{Entity: "e"}}
	res := c.Send(context.Background(), n)
	require.False(t, res.OK)
	require.Error(t, res.Err)
}

// readOneNotifyFile reads the single file WriteNotify dropped into
// dir/unread -- every test above sends exactly one notification, so
// exactly one file is expected.
func readOneNotifyFile(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "unread"))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	b, err := os.ReadFile(filepath.Join(dir, "unread", entries[0].Name()))
	require.NoError(t, err)
	return string(b)
}
