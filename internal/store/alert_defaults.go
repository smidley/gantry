package store

// DefaultAlertRules returns Gantry's twelve built-in alert rules,
// verbatim from the Phase 4 design (docs/superpowers/plans/2026-08-29-
// phase-4-alerts-and-release.md, Task 5): seven threshold rules whose
// (warn, threshold, critical) triple is copied from thresholds.ts'
// matching display-band family's existing (warn, serious, critical)
// numbers -- no number here is new, so the alert and the color a value
// already showed can never disagree -- plus five event rules driven by
// the severity the collector already assigns an event (container.die/
// health's own warning/alert calls in docker/registry.go, not a second
// layer of string parsing here).
//
// Two deliberate traps, both from the plan:
//   - array-stopped uses op "<" on the 1/0 array.started metric rather
//     than an array.state event rule: array.state only fires on a
//     transition, so a box that booted with the array already stopped
//     would never emit one, while array.started is recorded every tick.
//   - container-exit-nonzero and container-unhealthy need no exit-code
//     or health-string parsing of their own -- min_severity alone does
//     the predicate work, because docker/registry.go's diffEvents/
//     translateEvent already assign container.die severity "warning"
//     only for a nonzero exit and container.health severity "warning"
//     only for "unhealthy". The one honest gap: diffEvents' 10s poll
//     path emits container.die at severity "info" with no exit code
//     when the event-stream path has a gap, so a die caught only by the
//     poll never clears this rule's floor -- degradation, not a bug.
//
// UpdatedAt is left 0 on every rule; SeedAlertRules stamps it at insert
// time, the same way AppendEvent/AddSilence stamp their own timestamp
// fields when the caller leaves them zero.
func DefaultAlertRules() []AlertRule {
	return []AlertRule{
		{
			ID: "host-cpu-high", Name: "Host CPU high", Enabled: true, Builtin: true,
			Type: "threshold", Kind: "host", EntityGlob: "*",
			Metric: "cpu.total", Op: ">", Threshold: 85, ClearThreshold: 70,
			WarnThreshold: 70, CriticalThreshold: 95, BandFamily: "host.cpu",
			ForSeconds: 600, ClearSeconds: 300, Severity: "warning",
		},
		{
			ID: "host-mem-high", Name: "Host memory high", Enabled: true, Builtin: true,
			Type: "threshold", Kind: "host", EntityGlob: "*",
			Metric: "mem.used_pct", Op: ">", Threshold: 85, ClearThreshold: 70,
			WarnThreshold: 70, CriticalThreshold: 95, BandFamily: "host.mem",
			ForSeconds: 600, ClearSeconds: 300, Severity: "warning",
		},
		{
			// fs.used_pct doesn't exist as a persisted metric until Task 2
			// (internal/collect/unraid/disks.go) lands -- this rule seeds
			// with everyone else regardless; no series simply means no
			// evaluation, the same "absent series, no alarm" degradation
			// every other rule already tolerates when its metric hasn't
			// reported yet.
			ID: "disk-usage-high", Name: "Disk usage high", Enabled: true, Builtin: true,
			Type: "threshold", Kind: "disk", EntityGlob: "*",
			Metric: "fs.used_pct", Op: ">", Threshold: 90, ClearThreshold: 85,
			WarnThreshold: 70, CriticalThreshold: 95, BandFamily: "disk.capacity",
			ForSeconds: 900, ClearSeconds: 900, Severity: "warning", RenotifyHours: 24,
		},
		{
			// entity_class "!nvme": disk-class scoping is Task 3's
			// MatchClass evaluator; this row only carries the data.
			ID: "disk-temp-high", Name: "Disk temperature high", Enabled: true, Builtin: true,
			Type: "threshold", Kind: "disk", EntityGlob: "*", EntityClass: "!nvme",
			Metric: "temp.c", Op: ">", Threshold: 55, ClearThreshold: 50,
			WarnThreshold: 45, BandFamily: "disk.temp",
			ForSeconds: 600, ClearSeconds: 600, Severity: "warning", RenotifyHours: 12,
		},
		{
			ID: "disk-temp-nvme-high", Name: "NVMe temperature high", Enabled: true, Builtin: true,
			Type: "threshold", Kind: "disk", EntityGlob: "*", EntityClass: "nvme",
			Metric: "temp.c", Op: ">", Threshold: 70, ClearThreshold: 65,
			WarnThreshold: 60, BandFamily: "disk.temp.nvme",
			ForSeconds: 600, ClearSeconds: 600, Severity: "warning", RenotifyHours: 12,
		},
		{
			// mem.limit_pct self-scopes: it's only ever emitted for a
			// container that actually has a memory limit set, so no
			// series already means no evaluation -- nothing extra to
			// filter here.
			ID: "container-mem-limit-high", Name: "Container memory limit high", Enabled: true, Builtin: true,
			Type: "threshold", Kind: "container", EntityGlob: "*",
			Metric: "mem.limit_pct", Op: ">", Threshold: 85, ClearThreshold: 75,
			WarnThreshold: 75, CriticalThreshold: 95, BandFamily: "container.mem_limit_pct",
			ForSeconds: 600, ClearSeconds: 600, Severity: "warning",
		},
		{
			ID: "array-stopped", Name: "Array stopped", Enabled: true, Builtin: true,
			Type: "threshold", Kind: "unraid", EntityGlob: "array",
			Metric: "array.started", Op: "<", Threshold: 1, ClearThreshold: 0,
			ForSeconds: 300, ClearSeconds: 60, Severity: "alert", RenotifyHours: 24,
		},
		{
			ID: "container-unhealthy", Name: "Container unhealthy", Enabled: true, Builtin: true,
			Type: "event", Kind: "container", EntityGlob: "*",
			EventKinds: "container.health", MinSeverity: "warning",
			ClearEventKinds: "container.health", ClearMaxSeverity: "info",
			ClearSeconds: 21600, Severity: "alert", RenotifyHours: 24,
		},
		{
			ID: "container-oom", Name: "Container OOM-killed", Enabled: true, Builtin: true,
			Type: "event", Kind: "container", EntityGlob: "*",
			EventKinds: "container.oom", MinSeverity: "alert",
			ClearSeconds: 3600, Severity: "alert",
		},
		{
			ID: "container-exit-nonzero", Name: "Container exited nonzero", Enabled: true, Builtin: true,
			Type: "event", Kind: "container", EntityGlob: "*",
			EventKinds: "container.die", MinSeverity: "warning",
			ClearSeconds: 3600, Severity: "warning",
		},
		{
			ID: "disk-errors", Name: "Disk errors", Enabled: true, Builtin: true,
			Type: "event", Kind: "disk", EntityGlob: "*",
			EventKinds: "disk.errors", MinSeverity: "alert",
			ClearSeconds: 86400, Severity: "alert", RenotifyHours: 24,
		},
		{
			// parity.finish's Severity only ever reads "info" until Task 2
			// enriches it on sbSyncErrs > 0 -- min_severity "warning" is
			// forward-looking, same "seeds now, tolerates absence" posture
			// as disk-usage-high's fs.used_pct above.
			ID: "parity-errors", Name: "Parity check errors", Enabled: true, Builtin: true,
			Type: "event", Kind: "unraid", EntityGlob: "array",
			EventKinds: "parity.finish", MinSeverity: "warning",
			ClearSeconds: 86400, Severity: "alert",
		},
	}
}

// SeedAlertRules inserts every rule in defaults whose id is not already
// present in alert_rules, leaving any existing row -- edited, disabled,
// or otherwise -- completely untouched. It never updates and never
// deletes: the per-id existence check IS the upgrade mechanism the plan
// calls for. A rule already seeded keeps whatever the user did to it
// forever; a new default introduced by a later upgrade is simply absent
// from an existing DB, so it inserts on the very next call with no
// separate version marker to maintain. Idempotent: calling it twice (or
// on every boot, which is the actual caller) inserts each rule at most
// once. Run before the engine's first tick (there is no engine yet in
// this phase's Task 1/5; main.go calls this immediately after the store
// opens).
func (s *Store) SeedAlertRules(defaults []AlertRule) error {
	now := s.clock().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for _, r := range defaults {
		if r.UpdatedAt == 0 {
			r.UpdatedAt = now
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO alert_rules (`+alertRuleColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			r.ID, r.Name, r.Enabled, r.Builtin, r.Type, r.Kind, r.EntityGlob, r.EntityClass, r.Metric, r.Op,
			r.Threshold, r.ClearThreshold, r.WarnThreshold, r.CriticalThreshold, r.BandFamily,
			r.ForSeconds, r.ClearSeconds, r.EventKinds, r.MinSeverity, r.ClearEventKinds,
			r.ClearMaxSeverity, r.Severity, r.Channels, r.RenotifyHours, r.UpdatedAt); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
