package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// wantDefaultAlertRules is the plan's own 12-rule table, transcribed
// field-for-field (Phase 4 plan, Task 5: "every threshold rule's
// `threshold` is its `thresholds.ts` family's existing serious boundary,
// and `warn_threshold`/`critical_threshold` are that family's existing
// warn/critical -- no number in this table is new"). Every field not
// listed for a given rule is intentionally the Go zero value ("", 0,
// false) -- e.g. an event rule has no Metric/Threshold/ForSeconds, a
// non-banded rule (array-stopped) has no BandFamily/WarnThreshold.
// UpdatedAt is 0 here; SeedAlertRules stamps it at insert time.
func wantDefaultAlertRules() map[string]AlertRule {
	return map[string]AlertRule{
		"host-cpu-high": {
			ID: "host-cpu-high", Name: "Host CPU high", Enabled: true, Builtin: true,
			Type: "threshold", Kind: "host", EntityGlob: "*",
			Metric: "cpu.total", Op: ">", Threshold: 85, ClearThreshold: 70,
			WarnThreshold: 70, CriticalThreshold: 95, BandFamily: "host.cpu",
			ForSeconds: 600, ClearSeconds: 300, Severity: "warning",
		},
		"host-mem-high": {
			ID: "host-mem-high", Name: "Host memory high", Enabled: true, Builtin: true,
			Type: "threshold", Kind: "host", EntityGlob: "*",
			Metric: "mem.used_pct", Op: ">", Threshold: 85, ClearThreshold: 70,
			WarnThreshold: 70, CriticalThreshold: 95, BandFamily: "host.mem",
			ForSeconds: 600, ClearSeconds: 300, Severity: "warning",
		},
		"disk-usage-high": {
			ID: "disk-usage-high", Name: "Disk usage high", Enabled: true, Builtin: true,
			Type: "threshold", Kind: "disk", EntityGlob: "*",
			Metric: "fs.used_pct", Op: ">", Threshold: 90, ClearThreshold: 85,
			WarnThreshold: 70, CriticalThreshold: 95, BandFamily: "disk.capacity",
			ForSeconds: 900, ClearSeconds: 900, Severity: "warning", RenotifyHours: 24,
		},
		"disk-temp-high": {
			ID: "disk-temp-high", Name: "Disk temperature high", Enabled: true, Builtin: true,
			Type: "threshold", Kind: "disk", EntityGlob: "*", EntityClass: "!nvme",
			Metric: "temp.c", Op: ">", Threshold: 55, ClearThreshold: 50,
			WarnThreshold: 45, BandFamily: "disk.temp",
			ForSeconds: 600, ClearSeconds: 600, Severity: "warning", RenotifyHours: 12,
		},
		"disk-temp-nvme-high": {
			ID: "disk-temp-nvme-high", Name: "NVMe temperature high", Enabled: true, Builtin: true,
			Type: "threshold", Kind: "disk", EntityGlob: "*", EntityClass: "nvme",
			Metric: "temp.c", Op: ">", Threshold: 70, ClearThreshold: 65,
			WarnThreshold: 60, BandFamily: "disk.temp.nvme",
			ForSeconds: 600, ClearSeconds: 600, Severity: "warning", RenotifyHours: 12,
		},
		"container-mem-limit-high": {
			ID: "container-mem-limit-high", Name: "Container memory limit high", Enabled: true, Builtin: true,
			Type: "threshold", Kind: "container", EntityGlob: "*",
			Metric: "mem.limit_pct", Op: ">", Threshold: 85, ClearThreshold: 75,
			WarnThreshold: 75, CriticalThreshold: 95, BandFamily: "container.mem_limit_pct",
			ForSeconds: 600, ClearSeconds: 600, Severity: "warning",
		},
		"array-stopped": {
			ID: "array-stopped", Name: "Array stopped", Enabled: true, Builtin: true,
			Type: "threshold", Kind: "unraid", EntityGlob: "array",
			Metric: "array.started", Op: "<", Threshold: 1, ClearThreshold: 0,
			ForSeconds: 300, ClearSeconds: 60, Severity: "alert", RenotifyHours: 24,
		},
		"container-unhealthy": {
			ID: "container-unhealthy", Name: "Container unhealthy", Enabled: true, Builtin: true,
			Type: "event", Kind: "container", EntityGlob: "*",
			EventKinds: "container.health", MinSeverity: "warning",
			ClearEventKinds: "container.health", ClearMaxSeverity: "info",
			ClearSeconds: 21600, Severity: "alert", RenotifyHours: 24,
		},
		"container-oom": {
			ID: "container-oom", Name: "Container OOM-killed", Enabled: true, Builtin: true,
			Type: "event", Kind: "container", EntityGlob: "*",
			EventKinds: "container.oom", MinSeverity: "alert",
			ClearSeconds: 3600, Severity: "alert",
		},
		"container-exit-nonzero": {
			ID: "container-exit-nonzero", Name: "Container exited nonzero", Enabled: true, Builtin: true,
			Type: "event", Kind: "container", EntityGlob: "*",
			EventKinds: "container.die", MinSeverity: "warning",
			ClearSeconds: 3600, Severity: "warning",
		},
		"disk-errors": {
			ID: "disk-errors", Name: "Disk errors", Enabled: true, Builtin: true,
			Type: "event", Kind: "disk", EntityGlob: "*",
			EventKinds: "disk.errors", MinSeverity: "alert",
			ClearSeconds: 86400, Severity: "alert", RenotifyHours: 24,
		},
		"parity-errors": {
			ID: "parity-errors", Name: "Parity check errors", Enabled: true, Builtin: true,
			Type: "event", Kind: "unraid", EntityGlob: "array",
			EventKinds: "parity.finish", MinSeverity: "warning",
			ClearSeconds: 86400, Severity: "alert",
		},
	}
}

// byID indexes a rule slice by ID for order-independent comparison --
// DefaultAlertRules' contract is "these twelve rules with these values,"
// never a promised slice order.
func byID(rules []AlertRule) map[string]AlertRule {
	out := make(map[string]AlertRule, len(rules))
	for _, r := range rules {
		out[r.ID] = r
	}
	return out
}

// TestDefaultAlertRulesExactValues is the pinned table: every field of
// every one of the twelve defaults, checked individually by id so a
// failure names exactly which rule and (via require.Equal's diff) which
// field drifted from the plan's table.
func TestDefaultAlertRulesExactValues(t *testing.T) {
	want := wantDefaultAlertRules()
	got := byID(DefaultAlertRules(false))

	require.Len(t, got, len(want), "must be exactly the twelve defaults, no more, no fewer")
	for id, w := range want {
		g, ok := got[id]
		require.True(t, ok, "missing default rule %q", id)
		require.Equal(t, w, g, "rule %q", id)
	}
}

// TestDefaultAlertRulesHasTwelveUniqueIDs guards against a copy-paste id
// collision silently shrinking the set (byID would otherwise mask a
// duplicate by just overwriting the map entry).
func TestDefaultAlertRulesHasTwelveUniqueIDs(t *testing.T) {
	rules := DefaultAlertRules(false)
	require.Len(t, rules, 12)
	seen := make(map[string]bool, len(rules))
	for _, r := range rules {
		require.False(t, seen[r.ID], "duplicate id %q", r.ID)
		seen[r.ID] = true
	}
}

// TestSeedAlertRulesInsertsAllDefaultsOnFreshDB pins the boot-time happy
// path: an empty alert_rules table ends up with all twelve after one
// SeedAlertRules call.
func TestSeedAlertRulesInsertsAllDefaultsOnFreshDB(t *testing.T) {
	s := newTestStore(t, nil)
	require.NoError(t, s.SeedAlertRules(DefaultAlertRules(false)))

	got, err := s.AlertRules(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 12)
	for _, r := range got {
		require.True(t, r.Builtin, "every seeded default must be builtin")
		require.True(t, r.Enabled, "every seeded default starts enabled")
		require.NotZero(t, r.UpdatedAt, "SeedAlertRules must stamp updated_at")
	}
}

// TestSeedAlertRulesIsIdempotent pins "seeding twice inserts once" --
// Task 5's own stated test.
func TestSeedAlertRulesIsIdempotent(t *testing.T) {
	s := newTestStore(t, nil)
	require.NoError(t, s.SeedAlertRules(DefaultAlertRules(false)))
	require.NoError(t, s.SeedAlertRules(DefaultAlertRules(false)))

	got, err := s.AlertRules(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 12, "a second seed call must not duplicate any row")
}

// TestSeedAlertRulesNeverOverwritesAnEditedOrDisabledBuiltin pins the
// upgrade-safety contract: SeedAlertRules only ever inserts a row whose
// id is absent -- it never touches a row that already exists, however
// the user left it (edited, disabled, or both).
func TestSeedAlertRulesNeverOverwritesAnEditedOrDisabledBuiltin(t *testing.T) {
	s := newTestStore(t, nil)
	require.NoError(t, s.SeedAlertRules(DefaultAlertRules(false)))

	rules, err := s.AlertRules(context.Background())
	require.NoError(t, err)
	edited := byID(rules)["host-cpu-high"]
	edited.Enabled = false
	edited.Threshold = 999
	require.NoError(t, s.UpsertAlertRule(edited))

	require.NoError(t, s.SeedAlertRules(DefaultAlertRules(false)))

	got, err := s.AlertRules(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 12, "re-seeding must not add a duplicate row for an already-seeded id")
	require.Equal(t, edited, byID(got)["host-cpu-high"], "re-seeding must not resurrect the disabled state or the edited threshold")
}

// TestSeedAlertRulesInsertsANewlyAddedDefaultOnNextBoot pins the actual
// upgrade mechanism: seeding is per-rule-id absence, not a global
// version marker, so a default introduced by a later Gantry upgrade
// (simulated here as a subset seeded first, then the full list on a
// second "boot") is simply inserted the next time SeedAlertRules runs
// -- while an already-seeded, already-edited row is left untouched.
func TestSeedAlertRulesInsertsANewlyAddedDefaultOnNextBoot(t *testing.T) {
	s := newTestStore(t, nil)
	all := DefaultAlertRules(false)
	upgradeIntroduced := all[len(all)-1] // "parity-errors": absent from the "old" install below
	oldInstall := all[:len(all)-1]

	require.NoError(t, s.SeedAlertRules(oldInstall))

	rules, err := s.AlertRules(context.Background())
	require.NoError(t, err)
	require.Len(t, rules, 11)
	edited := byID(rules)["host-cpu-high"]
	edited.Threshold = 999
	require.NoError(t, s.UpsertAlertRule(edited))

	require.NoError(t, s.SeedAlertRules(all)) // "upgrade": boot with the full, current default list

	got, err := s.AlertRules(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 12)
	newRow, ok := byID(got)[upgradeIntroduced.ID]
	require.True(t, ok, "the newly-added default must be inserted on this boot")
	require.Equal(t, upgradeIntroduced.EventKinds, newRow.EventKinds)
	require.Equal(t, upgradeIntroduced.MinSeverity, newRow.MinSeverity)
	require.Equal(t, upgradeIntroduced.ClearSeconds, newRow.ClearSeconds)
	require.Equal(t, upgradeIntroduced.Severity, newRow.Severity)
	require.Equal(t, 999.0, byID(got)["host-cpu-high"].Threshold, "an already-seeded edited rule must survive the same boot untouched")
}

// TestDefaultAlertRulesFastCompressesThresholdWindowsOnly pins Task 9's
// fake-mode contract: fast=true rewrites every THRESHOLD rule's
// for_seconds/clear_seconds to 60/60, leaves every other field --
// including an event rule's own ClearSeconds timeout -- byte-identical
// to fast=false, so the demo fires against the exact same numbers
// thresholds.ts's bands show, just sustained for less real time.
func TestDefaultAlertRulesFastCompressesThresholdWindowsOnly(t *testing.T) {
	slow := byID(DefaultAlertRules(false))
	fast := byID(DefaultAlertRules(true))
	require.Len(t, fast, len(slow))

	for id, s := range slow {
		f, ok := fast[id]
		require.True(t, ok, "fast mode must seed the same rule set, missing %q", id)

		if s.Type == "threshold" {
			require.Equal(t, int64(60), f.ForSeconds, "rule %q: fast mode must compress for_seconds to 60", id)
			require.Equal(t, int64(60), f.ClearSeconds, "rule %q: fast mode must compress clear_seconds to 60", id)
			// Everything else about a threshold rule -- the numbers that
			// decide WHAT breaches, not how long it must sustain -- is
			// untouched: zero out just the two compressed fields and the
			// rest must compare equal.
			f.ForSeconds, f.ClearSeconds = s.ForSeconds, s.ClearSeconds
			require.Equal(t, s, f, "rule %q: fast mode must not change anything but the two window fields", id)
		} else {
			require.Equal(t, s, f, "rule %q: fast mode must leave event rules completely untouched (their ClearSeconds is a timeout, not a sustained-for window)", id)
		}
	}
}
