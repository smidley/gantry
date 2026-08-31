package insight

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

// --- fakeInsightStore ------------------------------------------------------

// fakeInsightStore is a minimal in-memory double for the engine's own
// narrow Store interface -- real enough to drive the lifecycle (active/
// resolved split, event ordering) without a real SQLite file.
type fakeInsightStore struct {
	instances  map[int64]store.InsightInstance
	nextID     int64
	configs    []store.InsightRuleConfig
	dismissals []store.InsightDismissal
	events     []store.Event
}

func newFakeInsightStore() *fakeInsightStore {
	return &fakeInsightStore{instances: map[int64]store.InsightInstance{}}
}

func (s *fakeInsightStore) ActiveInsights(context.Context) ([]store.InsightInstance, error) {
	var out []store.InsightInstance
	for _, i := range s.instances {
		if i.ResolvedAt == 0 {
			out = append(out, i)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID < out[b].ID })
	return out, nil
}

func (s *fakeInsightStore) UpsertInsight(i store.InsightInstance) (int64, error) {
	if i.ID == 0 {
		s.nextID++
		i.ID = s.nextID
	}
	s.instances[i.ID] = i
	return i.ID, nil
}

func (s *fakeInsightStore) ResolveInsight(id, at int64, reason string) error {
	inst, ok := s.instances[id]
	if !ok {
		return errNotFound
	}
	inst.State, inst.ResolvedAt, inst.ResolveReason = "resolved", at, reason
	s.instances[id] = inst
	return nil
}

func (s *fakeInsightStore) InsightRuleConfigs(context.Context) ([]store.InsightRuleConfig, error) {
	return s.configs, nil
}

func (s *fakeInsightStore) InsightDismissals(_ context.Context, now int64) ([]store.InsightDismissal, error) {
	var out []store.InsightDismissal
	for _, d := range s.dismissals {
		if d.Until > now {
			out = append(out, d)
		}
	}
	return out, nil
}

func (s *fakeInsightStore) AppendEvent(e store.Event) (int64, error) {
	s.events = append(s.events, e)
	return int64(len(s.events)), nil
}

func (s *fakeInsightStore) QueryEvents(_ context.Context, f store.EventFilter) ([]store.Event, error) {
	var out []store.Event
	for _, e := range s.events {
		if len(f.Kinds) > 0 && !containsStr(f.Kinds, e.Kind) {
			continue
		}
		if f.From > 0 && e.TS < f.From {
			continue
		}
		if f.To > 0 && e.TS > f.To {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

type simpleErr string

func (e simpleErr) Error() string { return string(e) }

const errNotFound = simpleErr("insight instance not found")

// --- shared fixture: memory-squeeze via an OOM event ----------------------

// newOOMEngine wires an Engine over a fakeInsightStore and a real
// store.Live (so MatchSince/MatchPrefixSince are the actual, already-
// tested implementation, not a hand-rolled stand-in), with a
// mutable clock the test advances directly. redis' mem.pct clears
// memory-squeeze's own culprit floor throughout.
func newOOMEngine(t *testing.T) (*Engine, *fakeInsightStore, *store.Live, *time.Time) {
	t.Helper()
	live := store.NewLive(2000)
	fs := newFakeInsightStore()
	now := time.Unix(2_000_000, 0)

	eng := New(fs)
	eng.MatchSince = live.MatchSince
	eng.MatchPrefixSince = live.MatchPrefixSince
	eng.Clock = func() time.Time { return now }
	eng.ClearForSecs = 180
	eng.CooldownSecs = 1800

	// Recorded well past every test's own clock advances (up to ~35
	// minutes across the cooldown test): a fixture concern, not
	// something a real box would ever need -- collectors record every
	// tick, so redis' mem.pct would always be fresh relative to
	// whatever "now" the engine asks about.
	for ts := now.Unix() - 200; ts <= now.Unix()+3000; ts += 10 {
		live.Record(store.SeriesKey{Kind: "container", Entity: "redis", Metric: "mem.pct"}, ts, 42)
	}
	return eng, fs, live, &now
}

func fireOOM(fs *fakeInsightStore, ts int64) {
	fs.events = append(fs.events, store.Event{TS: ts, Kind: "container.oom", Entity: "minecraft", Severity: "alert"})
}

// --- lifecycle -------------------------------------------------------

func TestEngineTickCreatesActiveInsightAndAppendsDetectedEvent(t *testing.T) {
	eng, fs, _, now := newOOMEngine(t)
	fireOOM(fs, now.Unix())

	require.NoError(t, eng.Tick(context.Background()))

	active, err := fs.ActiveInsights(context.Background())
	require.NoError(t, err)
	require.Len(t, active, 1)
	inst := active[0]
	require.Equal(t, RuleMemorySqueeze, inst.RuleID)
	require.Equal(t, "minecraft", inst.Victim)
	require.Equal(t, "active", inst.State)
	require.Equal(t, "confirmed", inst.Confidence)
	require.Equal(t, "alert", inst.Severity)
	require.Equal(t, "redis", inst.Culprit)

	detected := eventsOfKind(fs.events, "insight.detected")
	require.Len(t, detected, 1)
	require.Equal(t, "minecraft", detected[0].Entity)
	require.Equal(t, inst.Statement, detected[0].Detail)
}

// eventsOfKind filters the fake store's raw event log (which, in these
// tests, also holds the manually-injected container.oom "real" events
// the engine itself never appends) down to just the engine's own
// insight.* transitions.
func eventsOfKind(events []store.Event, kind string) []store.Event {
	var out []store.Event
	for _, e := range events {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func TestEngineRepeatedTicksUpdateTheSameInstanceNotANewOne(t *testing.T) {
	eng, fs, _, now := newOOMEngine(t)
	fireOOM(fs, now.Unix())

	require.NoError(t, eng.Tick(context.Background()))
	first, err := fs.ActiveInsights(context.Background())
	require.NoError(t, err)
	require.Len(t, first, 1)

	*now = now.Add(30 * time.Second)
	require.NoError(t, eng.Tick(context.Background()))

	second, err := fs.ActiveInsights(context.Background())
	require.NoError(t, err)
	require.Len(t, second, 1)
	require.Equal(t, first[0].ID, second[0].ID)
	// Only the FIRST tick's fire produces a detected event.
	require.Len(t, eventsOfKind(fs.events, "insight.detected"), 1)
}

func TestEngineResolvesAfterClearForElapsesWithSameInstanceIDAndAppendsResolvedEvent(t *testing.T) {
	eng, fs, _, now := newOOMEngine(t)
	fireOOM(fs, now.Unix())
	require.NoError(t, eng.Tick(context.Background()))
	active, _ := fs.ActiveInsights(context.Background())
	firedID := active[0].ID

	// The OOM event ages out of the 120s evidence window well before
	// ClearForSecs (180s) elapses -- Eval stops returning a Finding for
	// this tuple, but the clear-for grace period is what actually
	// resolves it, not the very next tick it goes missing.
	*now = now.Add(150 * time.Second)
	require.NoError(t, eng.Tick(context.Background()))
	stillActive, _ := fs.ActiveInsights(context.Background())
	require.Len(t, stillActive, 1, "must not resolve before ClearForSecs has elapsed since it was last seen")

	*now = now.Add(60 * time.Second) // total 210s since last seen > 180s ClearForSecs
	require.NoError(t, eng.Tick(context.Background()))

	afterClear, _ := fs.ActiveInsights(context.Background())
	require.Empty(t, afterClear)
	resolved := fs.instances[firedID]
	require.Equal(t, "resolved", resolved.State)
	require.Equal(t, "cleared", resolved.ResolveReason)

	var resolvedEvents int
	for _, e := range fs.events {
		if e.Kind == "insight.resolved" {
			resolvedEvents++
			require.Equal(t, "minecraft", e.Entity)
		}
	}
	require.Equal(t, 1, resolvedEvents)
}

func TestEngineCooldownBlocksImmediateRefireThenAllowsItAfterCooldown(t *testing.T) {
	eng, fs, _, now := newOOMEngine(t)
	fireOOM(fs, now.Unix())
	require.NoError(t, eng.Tick(context.Background()))
	firstActive, _ := fs.ActiveInsights(context.Background())
	firstID := firstActive[0].ID

	// Clear it.
	*now = now.Add(210 * time.Second)
	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, "resolved", fs.instances[firstID].State)

	// A fresh OOM on the SAME container, well inside the 1800s cooldown.
	*now = now.Add(60 * time.Second)
	fireOOM(fs, now.Unix())
	require.NoError(t, eng.Tick(context.Background()))
	blocked, _ := fs.ActiveInsights(context.Background())
	require.Empty(t, blocked, "the cooldown must block an immediate re-fire of the same tuple")

	// Past the cooldown, the SAME still-fresh-enough event (or a new one) fires again.
	*now = now.Add(1800 * time.Second)
	fireOOM(fs, now.Unix())
	require.NoError(t, eng.Tick(context.Background()))
	refired, _ := fs.ActiveInsights(context.Background())
	require.Len(t, refired, 1, "past cooldown, the tuple must be able to fire again")
	require.NotEqual(t, firstID, refired[0].ID, "a genuinely new occurrence after a real resolve gets a new row")
}

func TestEngineDismissalSuppressesWouldBeFireAndExpires(t *testing.T) {
	eng, fs, _, now := newOOMEngine(t)
	fs.dismissals = []store.InsightDismissal{
		{RuleID: RuleMemorySqueeze, Victim: "minecraft", Until: now.Unix() + 100},
	}
	fireOOM(fs, now.Unix())

	require.NoError(t, eng.Tick(context.Background()))
	active, _ := fs.ActiveInsights(context.Background())
	require.Empty(t, active, "a matching dismissal must suppress the would-be fire")

	*now = now.Add(150 * time.Second) // past the dismissal's `until`
	fireOOM(fs, now.Unix())
	require.NoError(t, eng.Tick(context.Background()))
	active, _ = fs.ActiveInsights(context.Background())
	require.Len(t, active, 1, "an expired dismissal must no longer suppress")
}

func TestEngineRuleDisabledMidFlightResolvesActivesWithRuleDisabled(t *testing.T) {
	eng, fs, _, now := newOOMEngine(t)
	fireOOM(fs, now.Unix())
	require.NoError(t, eng.Tick(context.Background()))
	active, _ := fs.ActiveInsights(context.Background())
	require.Len(t, active, 1)

	fs.configs = []store.InsightRuleConfig{{RuleID: RuleMemorySqueeze, Enabled: false}}
	require.NoError(t, eng.Tick(context.Background()))

	stillActive, _ := fs.ActiveInsights(context.Background())
	require.Empty(t, stillActive)
	require.Equal(t, "rule-disabled", fs.instances[active[0].ID].ResolveReason)
}

// --- seam invariant 7: never both a single- and shared-culprit row --------

// TestEngineSeamInvariant7NeverEmitsBothSingleAndSharedCulpritForSameTuple
// drives disk-io-contention through two ticks where the SAME device's
// culprit shape flips from a single dominant culprit to a shared pair --
// idx_insight_active's own uniqueness key includes culprit, so the schema
// alone would happily let both rows coexist; the engine must not.
func TestEngineSeamInvariant7NeverEmitsBothSingleAndSharedCulpritForSameTuple(t *testing.T) {
	live := store.NewLive(2000)
	fs := newFakeInsightStore()
	now := time.Unix(3_000_000, 0)

	eng := New(fs)
	eng.MatchSince = live.MatchSince
	eng.MatchPrefixSince = live.MatchPrefixSince
	eng.Clock = func() time.Time { return now }
	eng.Slots = func() map[string]SlotMeta { return map[string]SlotMeta{"disk3": {Device: "sde", Rotational: true}} }

	recordDiskIOContention := func(qbitShare, jellyfinShare float64) {
		for ts := now.Unix() - 700; ts <= now.Unix(); ts += 10 {
			live.Record(store.SeriesKey{Kind: "host", Metric: "diskio.sde.util_pct"}, ts, 97)
			val := 5.0
			if ts >= now.Unix()-120 {
				val = 45
			}
			live.Record(store.SeriesKey{Kind: "host", Metric: "diskio.sde.await_ms"}, ts, val)
		}
		for ts := now.Unix() - 100; ts <= now.Unix(); ts += 10 {
			live.Record(store.SeriesKey{Kind: "container", Entity: "qbittorrent", Metric: "live:io.sde.read_bps"}, ts, qbitShare)
			live.Record(store.SeriesKey{Kind: "container", Entity: "jellyfin", Metric: "live:io.sde.read_bps"}, ts, jellyfinShare)
		}
	}

	// Tick 1: qbittorrent alone clears the 60% floor (800 of 900).
	recordDiskIOContention(800, 100)
	require.NoError(t, eng.Tick(context.Background()))
	afterTick1, _ := fs.ActiveInsights(context.Background())
	require.Len(t, afterTick1, 1)
	require.Equal(t, "qbittorrent", afterTick1[0].Culprit)
	require.False(t, afterTick1[0].Culprit == "" && afterTick1[0].Culprits != "")
	singleID := afterTick1[0].ID

	// Tick 2: the shares shift so NEITHER alone clears 60%, but together
	// they do (440+310 of 1000) -- Dominant's own shared-pair shape. The
	// clock advances by MORE than the 120s evidence window so tick 1's
	// own (800/100) samples fall completely outside tick 2's own fetch
	// window -- otherwise Share's mean would blend both ticks' numbers
	// together, which is a fixture-cleanliness concern, not anything
	// the real engine needs (a real collector never double-writes the
	// same metric at two different values for the same instant).
	now = now.Add(300 * time.Second)
	recordDiskIOContention(440, 310)
	require.NoError(t, eng.Tick(context.Background()))

	afterTick2, _ := fs.ActiveInsights(context.Background())
	require.Len(t, afterTick2, 1, "exactly one active row for this (rule,victim,resource) -- never both shapes at once")
	require.True(t, afterTick2[0].Culprit == "" && afterTick2[0].Culprits != "", "tick 2 must be the shared-culprit shape")
	require.ElementsMatch(t, []string{"qbittorrent", "jellyfin"}, splitCSV(afterTick2[0].Culprits))

	// The tick-1 single-culprit row must be resolved SUPERSEDED, not
	// left dangling active and not counted as a real "cleared" event.
	require.Equal(t, "resolved", fs.instances[singleID].State)
	require.Equal(t, "superseded", fs.instances[singleID].ResolveReason)
	for _, e := range fs.events {
		require.NotEqual(t, "insight.resolved", e.Kind, "a superseded transition must never announce a fake resolution")
	}
}

func splitCSV(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// --- global cap ---------------------------------------------------------

func TestEngineGlobalCapKeepsHigherPriorityAndDropsTheRest(t *testing.T) {
	live := store.NewLive(2000)
	fs := newFakeInsightStore()
	now := time.Unix(4_000_000, 0)

	eng := New(fs)
	eng.MatchSince = live.MatchSince
	eng.MatchPrefixSince = live.MatchPrefixSince
	eng.Clock = func() time.Time { return now }
	eng.MaxActive = 3

	// 5 distinct memory-squeeze-by-OOM tuples (5 distinct victims), all
	// equal severity/confidence -- so the cap's started_at-ascending
	// tiebreak decides which 3 survive: the OLDEST 3 fired ticks.
	victims := []string{"v1", "v2", "v3", "v4", "v5"}
	for ts := now.Unix() - 100; ts <= now.Unix(); ts += 10 {
		live.Record(store.SeriesKey{Kind: "container", Entity: "redis", Metric: "mem.pct"}, ts, 42)
	}
	for i, v := range victims {
		fs.events = append(fs.events, store.Event{TS: now.Unix(), Kind: "container.oom", Entity: v, Severity: "alert"})
		require.NoError(t, eng.Tick(context.Background()))
		active, _ := fs.ActiveInsights(context.Background())
		if i < eng.MaxActive {
			require.Len(t, active, i+1, "under the cap, every new tuple stays active")
		} else {
			require.Len(t, active, eng.MaxActive, "over the cap, the newest excess is dropped")
		}
	}

	require.Equal(t, len(victims)-eng.MaxActive, eng.Dropped())
	active, _ := fs.ActiveInsights(context.Background())
	var gotVictims []string
	for _, a := range active {
		gotVictims = append(gotVictims, a.Victim)
	}
	require.ElementsMatch(t, victims[:eng.MaxActive], gotVictims, "the OLDEST tuples (earliest started_at) are the ones kept")
}

// --- gather: exactly one call per (kind, prefix)/(kind, metric) per tick --

func TestEngineGatherPerformsExactlyOnePrefixCallPerFamilyPerTick(t *testing.T) {
	fs := newFakeInsightStore()
	eng := New(fs)
	now := time.Unix(5_000_000, 0)
	eng.Clock = func() time.Time { return now }

	prefixCalls := map[string]int{}
	eng.MatchPrefixSince = func(kind, prefix string, since int64) (map[string]map[string][]store.Sample, map[string]map[string]int64) {
		prefixCalls[kind+"|"+prefix]++
		return nil, nil
	}
	matchCalls := map[string]int{}
	eng.MatchSince = func(kind, metric string, since int64) (map[string][]store.Sample, map[string]int64) {
		matchCalls[kind+"|"+metric]++
		return nil, nil
	}

	require.NoError(t, eng.Tick(context.Background()))

	require.Equal(t, 1, prefixCalls["container|live:io."], "exactly one MatchPrefixSince call for live:io. per tick, regardless of rule count")
	require.Equal(t, 1, prefixCalls["host|diskio."])
	require.Equal(t, 1, prefixCalls["gpu|engine."])
	require.Equal(t, 1, prefixCalls["container|gpu."])
	require.Len(t, prefixCalls, 4, "no other prefix family is ever queried")

	for key, n := range matchCalls {
		require.Equal(t, 1, n, "%s must be fetched exactly once per tick", key)
	}
}

// TestEngineGatherUsesMemSegmentNeverMemorySeamInvariant4 pins seam
// invariant 4: the PSI metric segment for memory is "mem", explicitly --
// pressure.go's own resources table maps the "memory" file/cgroup-file to
// metric segment "mem" (psi.mem.some_pct/full_pct), and that mapping must
// never be re-derived from the word "memory" itself anywhere in this
// engine. gather must ask Live for exactly "psi.mem.some_pct"/
// "psi.mem.full_pct" for both host and container, never "psi.memory.*".
func TestEngineGatherUsesMemSegmentNeverMemorySeamInvariant4(t *testing.T) {
	fs := newFakeInsightStore()
	eng := New(fs)
	now := time.Unix(6_000_000, 0)
	eng.Clock = func() time.Time { return now }

	var psiMetrics []string
	eng.MatchSince = func(kind, metric string, since int64) (map[string][]store.Sample, map[string]int64) {
		if strings.HasPrefix(metric, "psi.") {
			psiMetrics = append(psiMetrics, kind+"|"+metric)
		}
		return nil, nil
	}
	eng.MatchPrefixSince = func(kind, prefix string, since int64) (map[string]map[string][]store.Sample, map[string]map[string]int64) {
		return nil, nil
	}

	require.NoError(t, eng.Tick(context.Background()))

	require.Contains(t, psiMetrics, "host|psi.mem.some_pct")
	require.Contains(t, psiMetrics, "host|psi.mem.full_pct")
	require.Contains(t, psiMetrics, "container|psi.mem.some_pct")
	require.Contains(t, psiMetrics, "container|psi.mem.full_pct")
	for _, m := range psiMetrics {
		require.NotContains(t, m, "psi.memory.", "the memory PSI segment must be \"mem\", never \"memory\" (%s)", m)
	}
}

// --- Notifiable / dismissalMatches unit tests ------------------------------

func TestNotifiableRequiresAllThreeGates(t *testing.T) {
	confirmed := Finding{Confidence: ConfidenceConfirmed, Severity: "alert"}
	on := store.InsightRuleConfig{Notify: true}
	off := store.InsightRuleConfig{Notify: false}

	require.True(t, Notifiable(confirmed, on))
	require.False(t, Notifiable(confirmed, off), "notify defaults off")

	likely := Finding{Confidence: ConfidenceLikely, Severity: "alert"}
	require.False(t, Notifiable(likely, on), "a likely finding must never notify even with notify on and severity alert")

	warningConfirmed := Finding{Confidence: ConfidenceConfirmed, Severity: "warning"}
	require.False(t, Notifiable(warningConfirmed, on), "only severity alert may notify")
}

func TestNotifiableFalseForEverySeededRuleDefault(t *testing.T) {
	// Every seeded rule's default config has Notify false (Task 8) --
	// simulated here as the zero-value store.InsightRuleConfig a rule
	// with no explicit config row defaults to.
	for _, r := range DefaultRules() {
		f := Finding{RuleID: r.ID, Confidence: ConfidenceConfirmed, Severity: "alert"}
		require.False(t, Notifiable(f, store.InsightRuleConfig{}), "rule %s must not notify on the zero-value (unconfigured) config", r.ID)
	}
}

// TestDismissalMatchesSeamInvariant6 pins the per-axis "empty means any"
// contract and its one required exception.
func TestDismissalMatchesSeamInvariant6(t *testing.T) {
	require.True(t, dismissalMatches(
		store.InsightDismissal{RuleID: RuleDiskIOContention}, RuleDiskIOContention, "jellyfin", "qbittorrent", "disk3"),
		"empty victim/culprit/resource on the dismissal means any")

	require.False(t, dismissalMatches(
		store.InsightDismissal{RuleID: RuleDiskIOContention, Victim: "jellyfin"}, RuleDiskIOContention, "sonarr", "qbittorrent", "disk3"),
		"a non-empty axis must match exactly")

	require.False(t, dismissalMatches(store.InsightDismissal{}, RuleDiskIOContention, "jellyfin", "qbittorrent", "disk3"),
		"an all-empty dismissal matches NOTHING -- there is no scope:all marker column in 004_insights.sql to opt in with")
}
