package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/smidley/gantry/internal/alert"
	"github.com/smidley/gantry/internal/config"
	"github.com/smidley/gantry/internal/insight"
	"github.com/smidley/gantry/internal/server"
	"github.com/smidley/gantry/internal/store"
)

// settingsAdapter implements server.SettingsIface (Task 10): Get reuses
// store.RetentionFromConfig -- the same resolution the maintenance loop
// itself now calls every tick (see the per-tick comment above) -- so
// this can never drift from what's actually in effect, plus cfg's own
// env-override check per key; Set writes through to the settings table
// via st.SettingSet. Keeping this in main, not the server package,
// keeps server store/config-shape-agnostic the same way buildTop/
// buildContainersList already do for Query/Top/Containers.
type settingsAdapter struct {
	st  *store.Store
	cfg *config.Config
}

func (a settingsAdapter) Get() (server.RetentionSettings, map[string]bool) {
	ret := store.RetentionFromConfig(a.cfg.Int)
	out := server.RetentionSettings{
		R1Hours:   int(ret.R1 / time.Hour),
		R2Days:    int(ret.R2 / (24 * time.Hour)),
		R3Days:    int(ret.R3 / (24 * time.Hour)),
		SizeCapMB: int(ret.SizeCapBytes >> 20),
	}
	overridden := make(map[string]bool, len(retentionConfigKeys))
	for wire, key := range retentionConfigKeys {
		overridden[wire] = a.cfg.EnvOverridden(key)
	}
	return out, overridden
}

func (a settingsAdapter) Set(field string, value int) error {
	key, ok := retentionConfigKeys[field]
	if !ok {
		return fmt.Errorf("settings: unknown field %q", field) // unreached: handler only calls Set with its own whitelisted names
	}
	return a.st.SettingSet(key, strconv.Itoa(value))
}

// groupsSettingsKey is the one settings-table row every saved group
// lives under -- a single JSON-encoded blob, not one row per group
// (there's no per-field env-override dance to support the way
// retention's four keys have, so there's no reason to split it up).
const groupsSettingsKey = "groups"

// groupsAdapter implements server.GroupsIface: unlike settingsAdapter,
// this talks straight to st.SettingGet/SettingSet with no *config.Config
// in between -- groups are plain user data, not a tunable with an env
// var equivalent, so there's nothing for config's env>settings>default
// precedence to resolve. JSON marshal/unmarshal happens here, in main,
// the same "server package stays store-shape-agnostic" reasoning
// buildTop/buildContainersList/settingsAdapter itself already follow.
type groupsAdapter struct {
	st *store.Store
}

func (a groupsAdapter) Get() ([]server.Group, error) {
	raw, ok, err := a.st.SettingGet(groupsSettingsKey)
	if err != nil {
		return nil, err
	}
	if !ok {
		return []server.Group{}, nil
	}
	var groups []server.Group
	if err := json.Unmarshal([]byte(raw), &groups); err != nil {
		return nil, err
	}
	return groups, nil
}

func (a groupsAdapter) Set(groups []server.Group) error {
	raw, err := json.Marshal(groups)
	if err != nil {
		return err
	}
	return a.st.SettingSet(groupsSettingsKey, string(raw))
}

// overviewLayoutSettingsKey is the one settings-table row the saved
// Overview arrangement lives under -- a single JSON-encoded document,
// the same one-blob-one-key shape groupsSettingsKey above uses and for
// the same reason (no env-override precedence to resolve per field).
const overviewLayoutSettingsKey = "overview_layout"

// layoutAdapter implements server.LayoutIface over *store.Store,
// groupsAdapter's exact shape: straight SettingGet/SettingSet with no
// *config.Config in between (a layout is plain user data, not a tunable
// with an env var equivalent), and the JSON marshal/unmarshal kept here
// in main so the server package stays store-shape-agnostic.
//
// A row that has never been written returns the ZERO document rather
// than an error -- server.mergeOverviewLayout turns that into the
// default layout, so "never customized" needs no separate signal.
type layoutAdapter struct {
	st *store.Store
}

func (a layoutAdapter) Get() (server.OverviewLayout, error) {
	raw, ok, err := a.st.SettingGet(overviewLayoutSettingsKey)
	if err != nil {
		return server.OverviewLayout{}, err
	}
	if !ok {
		return server.OverviewLayout{}, nil
	}
	var layout server.OverviewLayout
	if err := json.Unmarshal([]byte(raw), &layout); err != nil {
		return server.OverviewLayout{}, err
	}
	return layout, nil
}

func (a layoutAdapter) Set(layout server.OverviewLayout) error {
	raw, err := json.Marshal(layout)
	if err != nil {
		return err
	}
	return a.st.SettingSet(overviewLayoutSettingsKey, string(raw))
}

// alertsAdapter implements server.AlertsIface (Task 8) over *store.Store
// plus the running *alert.Dispatcher's own Channels field for health --
// kept in main, not the server package, the same reason settingsAdapter
// is: server stays store/alert-shape-agnostic.
type alertsAdapter struct {
	st         *store.Store
	dispatcher *alert.Dispatcher
}

func (a alertsAdapter) Active(ctx context.Context) ([]store.AlertInstance, error) {
	return a.st.ActiveAlertInstances(ctx)
}

func (a alertsAdapter) History(ctx context.Context, from, to int64, limit int) ([]store.AlertInstance, error) {
	return a.st.AlertHistory(ctx, from, to, limit)
}

func (a alertsAdapter) Rules(ctx context.Context) ([]store.AlertRule, error) {
	return a.st.AlertRules(ctx)
}

func (a alertsAdapter) SaveRules(rules []store.AlertRule) error { return a.st.SaveAlertRules(rules) }

func (a alertsAdapter) Silences(ctx context.Context) ([]store.Silence, error) {
	return a.st.Silences(ctx, time.Now().Unix())
}

func (a alertsAdapter) AddSilence(sil store.Silence) (store.Silence, error) {
	id, err := a.st.AddSilence(sil)
	if err != nil {
		return store.Silence{}, err
	}
	sil.ID = id
	return sil, nil
}

func (a alertsAdapter) DeleteSilence(id int64) error { return a.st.DeleteSilence(id) }

func (a alertsAdapter) Channels() map[string]string { return channelHealthMap(a.dispatcher) }

// acksAdapter implements server.AcksIface over *store.Store -- kept in
// main, not the server package, the same reason alertsAdapter is:
// server stays store-shape-agnostic. Acks resolves "now" here (the
// alertsAdapter.Silences convention) so the server package never
// decides what expired means.
type acksAdapter struct {
	st *store.Store
}

func (a acksAdapter) Acks(ctx context.Context) ([]store.OverviewAck, error) {
	return a.st.Acks(ctx, time.Now().Unix())
}

func (a acksAdapter) AddAck(ack store.OverviewAck) (store.OverviewAck, error) {
	id, err := a.st.AddAck(ack)
	if err != nil {
		return store.OverviewAck{}, err
	}
	ack.ID = id
	return ack, nil
}

func (a acksAdapter) DeleteAck(id int64) error { return a.st.DeleteAck(id) }

// insightsAdapter implements server.InsightsIface (Phase 5 Task 9) over
// *store.Store plus the running *insight.Engine's own Dropped() and the
// pressure collector's Tier() -- kept in main, not the server package,
// the same reason alertsAdapter is: server stays store/engine-shape-
// agnostic. engine/pressureTier are both read live on every call rather
// than snapshotted once at construction, since both can change across
// this adapter's lifetime (Dropped() grows every tick; Tier() flips if
// /proc/pressure ever appears or disappears).
type insightsAdapter struct {
	st           *store.Store
	engine       *insight.Engine
	pressureTier func() string
}

func (a insightsAdapter) Active(ctx context.Context) ([]store.InsightInstance, error) {
	return a.st.ActiveInsights(ctx)
}

func (a insightsAdapter) ByID(ctx context.Context, id int64) (store.InsightInstance, bool, error) {
	return a.st.InsightByID(ctx, id)
}

func (a insightsAdapter) History(ctx context.Context, from, to int64, limit int) ([]store.InsightInstance, error) {
	return a.st.InsightHistory(ctx, from, to, limit)
}

func (a insightsAdapter) RuleConfigs(ctx context.Context) ([]store.InsightRuleConfig, error) {
	return a.st.InsightRuleConfigs(ctx)
}

func (a insightsAdapter) SaveRuleConfig(c store.InsightRuleConfig) error {
	return a.st.UpsertInsightRuleConfig(c)
}

func (a insightsAdapter) AddDismissal(d store.InsightDismissal) (int64, error) {
	return a.st.AddInsightDismissal(d)
}

func (a insightsAdapter) Resolve(id, at int64, reason string) error {
	return a.st.ResolveInsight(id, at, reason)
}

func (a insightsAdapter) Tier() string {
	if a.pressureTier == nil {
		return "proxy"
	}
	return a.pressureTier()
}

func (a insightsAdapter) Suppressed() int {
	if a.engine == nil {
		return 0
	}
	return a.engine.Dropped()
}

// webhooksAdapter implements server.WebhooksIface (Task 8) over the same
// settings-blob-backed target list Task 7 built (loadWebhookTargets/
// saveWebhookTargets), plus whether GANTRY_WEBHOOK_URL was set at boot
// (envLocked, resolved once in run() alongside readOnly).
type webhooksAdapter struct {
	st        *store.Store
	envLocked bool
}

func (a webhooksAdapter) Targets() ([]alert.WebhookTarget, bool, error) {
	targets, err := loadWebhookTargets(a.st)
	return targets, a.envLocked, err
}

func (a webhooksAdapter) Replace(targets []alert.WebhookTarget) error {
	return saveWebhookTargets(a.st, targets)
}

// DatabaseBytes reports allocated SQLite pages, excluding the transient WAL.
func (a settingsAdapter) DatabaseBytes() (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var size, count int64
	if err := a.st.ReadDB().QueryRowContext(ctx, "PRAGMA page_size").Scan(&size); err != nil {
		return 0, err
	}
	if err := a.st.ReadDB().QueryRowContext(ctx, "PRAGMA page_count").Scan(&count); err != nil {
		return 0, err
	}
	return size * count, nil
}
