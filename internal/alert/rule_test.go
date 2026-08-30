package alert

import (
	"testing"

	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

func TestMatchEntityStar(t *testing.T) {
	require.True(t, MatchEntity("*", "anything"))
	require.True(t, MatchEntity("*", ""))
}

func TestMatchEntityPrefix(t *testing.T) {
	require.True(t, MatchEntity("jelly*", "jellyfin"))
	require.True(t, MatchEntity("jelly*", "jelly"))
	require.False(t, MatchEntity("jelly*", "plex"))
	require.False(t, MatchEntity("jelly*", "notjellyfin"))
}

func TestMatchEntityExact(t *testing.T) {
	require.True(t, MatchEntity("disk3", "disk3"))
	require.False(t, MatchEntity("disk3", "disk4"))
	require.False(t, MatchEntity("disk3", "disk30"))
}

func TestMatchClassAnyWhenEmpty(t *testing.T) {
	require.True(t, MatchClass("", "nvme"))
	require.True(t, MatchClass("", "hdd"))
	require.True(t, MatchClass("", ""))
}

func TestMatchClassExact(t *testing.T) {
	require.True(t, MatchClass("nvme", "nvme"))
	require.False(t, MatchClass("nvme", "hdd"))
	require.False(t, MatchClass("nvme", ""))
}

func TestMatchClassNegation(t *testing.T) {
	require.False(t, MatchClass("!nvme", "nvme"))
	require.True(t, MatchClass("!nvme", "hdd"))
	require.True(t, MatchClass("!nvme", ""))
}

// validThreshold/validEvent are minimal valid rule literals so each
// ValidateRule test only has to override the one field it's checking.
func validThreshold() store.AlertRule {
	return store.AlertRule{
		ID: "r1", Name: "R1", Type: "threshold", Kind: "host", EntityGlob: "*",
		Metric: "cpu.total", Op: ">", Threshold: 85, ClearThreshold: 70,
		ForSeconds: 600, ClearSeconds: 300, Severity: "warning",
	}
}

func validEvent() store.AlertRule {
	return store.AlertRule{
		ID: "r2", Name: "R2", Type: "event", Kind: "container", EntityGlob: "*",
		EventKinds: "container.oom", MinSeverity: "alert", ClearSeconds: 3600, Severity: "alert",
	}
}

func TestValidateRuleAcceptsValidDefaults(t *testing.T) {
	require.NoError(t, ValidateRule(validThreshold()))
	require.NoError(t, ValidateRule(validEvent()))
}

func TestValidateRuleRejectsUnknownType(t *testing.T) {
	r := validThreshold()
	r.Type = "bogus"
	require.Error(t, ValidateRule(r))
}

func TestValidateRuleRejectsUnknownOp(t *testing.T) {
	r := validThreshold()
	r.Op = "!="
	require.Error(t, ValidateRule(r))
}

func TestValidateRuleRejectsUnknownSeverity(t *testing.T) {
	r := validThreshold()
	r.Severity = "urgent"
	require.Error(t, ValidateRule(r))
}

func TestValidateRuleRejectsForSecondsOverHourCap(t *testing.T) {
	r := validThreshold()
	r.ForSeconds = 3601
	require.Error(t, ValidateRule(r))
	r.ForSeconds = 3600
	require.NoError(t, ValidateRule(r))
}

func TestValidateRuleRejectsEqualThresholdAndClearWhenClearSecondsPositive(t *testing.T) {
	r := validThreshold()
	r.Threshold = 80
	r.ClearThreshold = 80
	r.ClearSeconds = 300
	require.Error(t, ValidateRule(r))
}

func TestValidateRuleAllowsEqualThresholdAndClearWhenClearSecondsZero(t *testing.T) {
	r := validThreshold()
	r.Threshold = 80
	r.ClearThreshold = 80
	r.ClearSeconds = 0
	require.NoError(t, ValidateRule(r))
}

func TestValidateRuleRejectsThresholdRuleWithNoMetric(t *testing.T) {
	r := validThreshold()
	r.Metric = ""
	require.Error(t, ValidateRule(r))
}

func TestValidateRuleRejectsEventRuleWithNoEventKinds(t *testing.T) {
	r := validEvent()
	r.EventKinds = ""
	require.Error(t, ValidateRule(r))
}

func TestValidateRuleRejectsRenotifyHoursOutOfRange(t *testing.T) {
	r := validThreshold()
	r.RenotifyHours = -1
	require.Error(t, ValidateRule(r))
	r.RenotifyHours = 169
	require.Error(t, ValidateRule(r))
	r.RenotifyHours = 168
	require.NoError(t, ValidateRule(r))
	r.RenotifyHours = 0
	require.NoError(t, ValidateRule(r))
}

func TestValidateRuleRejectsUnknownBandFamily(t *testing.T) {
	r := validThreshold()
	r.BandFamily = "made.up"
	require.Error(t, ValidateRule(r))
}

// TestValidateRuleAcceptsEveryDefaultRule pins the validator against the
// twelve real seeded rules (already on this branch's store package, Task
// 5) rather than just the pure fixtures above -- catches exactly the kind
// of mismatch a hand-built fixture would hide, e.g. an event rule's
// always-empty Op or Threshold/ClearThreshold both sitting at their zero
// value.
func TestValidateRuleAcceptsEveryDefaultRule(t *testing.T) {
	for _, r := range store.DefaultAlertRules() {
		require.NoError(t, ValidateRule(r), "default rule %q", r.ID)
	}
}

func TestValidateRuleAcceptsEveryKnownBandFamilyAndEmpty(t *testing.T) {
	r := validThreshold()
	for _, fam := range []string{"", "host.cpu", "host.mem", "disk.capacity", "disk.temp", "disk.temp.nvme", "container.mem_limit_pct"} {
		r.BandFamily = fam
		require.NoError(t, ValidateRule(r), "band family %q", fam)
	}
}
