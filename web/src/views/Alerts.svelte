<!--
  Alerts: active/history/rules for Gantry's alert engine (Task 10-11).
  Active is live off the SSE frame's own alerts.firing block -- no
  polling, the same "reads straight off the live frame" contract every
  other live surface in this app follows. History and the rule/webhook
  round trips are plain fetch/PUT, the same pattern Settings' retention
  editor and the Containers view's groups already use.

  D2 calm: severity color is otherwise reserved for something actually
  elevated everywhere else in this app -- the Active section is the ONE
  place it legitimately concentrates, since every row here is
  definitionally something that needs a look. Status is still never
  color-alone (HealthDot's own rule): every dot carries a text label.
-->
<script>
  import { onMount } from 'svelte';
  import { live } from '../lib/sse.svelte';
  import { alertRules } from '../lib/alertRules.svelte';
  import {
    fetchAlerts,
    fetchAlertHistory,
    fetchAlertRules,
    createSilence,
    deleteSilence,
  } from '../lib/api';
  import {
    describeResolveReason,
    firingDuration,
    silenceLabel,
    sortActiveAlerts,
    formatMetricValue,
    describeRule,
    alertEntityHref,
    channelLabel,
    SILENCE_PRESET_HOURS,
    annotateAlerts,
  } from '../lib/alerts';
  import HealthDot from '../components/HealthDot.svelte';
  import RuleEditor from '../components/RuleEditor.svelte';

  const HISTORY_PAGE_LIMIT = 25;

  // SEVERITY_STATUS mirrors EventFeedItem.svelte's own identical mapping
  // -- store.Event's three-slot severity vocabulary (info/warning/alert)
  // onto HealthDot's four-slot status vocabulary.
  const SEVERITY_STATUS = { info: 'good', warning: 'warning', alert: 'critical' };

  // --- Active -----------------------------------------------------------

  let firing = $derived(live.frame?.alerts?.firing ?? []);
  // annotated: the sanctioned insight->alert bridge (Phase 5 Task 13) --
  // reads straight off the SAME live frame's own insights.active block,
  // no extra fetch, so a fired/upgraded/resolved insight's "why" line
  // updates on the identical 2s cadence as everything else on this row.
  let annotated = $derived(annotateAlerts(firing, live.frame?.insights?.active ?? []));
  let activeSorted = $derived(sortActiveAlerts(annotated));
  let firingCount = $derived(live.frame?.alerts?.firing_count ?? 0);
  let truncated = $derived(live.frame?.alerts?.truncated ?? 0);

  // silences: fetched separately (the frame's own firing rows carry only
  // a `silenced` boolean, not the silence record itself -- its id, until,
  // and scope are what the Active row's "lift"/remaining-time UI needs).
  // Refreshed on mount and after every silence create/lift so the UI
  // reflects its own action immediately rather than waiting on the next
  // unrelated reload.
  let silences = $state([]);
  let silencesError = $state(false);
  async function loadSilences() {
    try {
      const resp = await fetchAlerts();
      silences = resp.silences;
      silencesError = false;
    } catch {
      silencesError = true;
    }
  }

  function coveringSilence(ruleId, entity) {
    return silences.find((s) => (s.rule_id === '' || s.rule_id === ruleId) && (s.entity === '' || s.entity === entity));
  }

  let openSilenceMenuKey = $state(null);
  let silenceActionError = $state(null);

  function rowKey(a) {
    return `${a.rule_id}|${a.entity}`;
  }

  async function silence(a, hours) {
    silenceActionError = null;
    try {
      await createSilence({ rule_id: a.rule_id, entity: a.entity, hours });
      openSilenceMenuKey = null;
      await loadSilences();
    } catch (err) {
      silenceActionError = err instanceof Error ? err.message : String(err);
    }
  }

  async function liftSilence(id) {
    silenceActionError = null;
    try {
      await deleteSilence(id);
      await loadSilences();
    } catch (err) {
      silenceActionError = err instanceof Error ? err.message : String(err);
    }
  }

  let nowSec = $state(Math.floor(Date.now() / 1000));
  let clockInterval;
  onMount(() => {
    clockInterval = setInterval(() => {
      nowSec = Math.floor(Date.now() / 1000);
    }, 1000);
    return () => clearInterval(clockInterval);
  });

  // --- Channels -----------------------------------------------------------

  let channels = $derived(live.frame?.alerts?.channels ?? {});
  let channelNames = $derived(Object.keys(channels).sort());

  // --- History --------------------------------------------------------------

  let history = $state([]);
  let historyLoading = $state(false);
  let historyFailed = $state(false);
  let historyHasMore = $state(false);
  let historyLoaded = $state(false);

  async function loadHistoryFirstPage() {
    historyLoading = true;
    historyFailed = false;
    try {
      const rows = await fetchAlertHistory({ limit: HISTORY_PAGE_LIMIT });
      history = rows;
      historyHasMore = rows.length === HISTORY_PAGE_LIMIT;
    } catch {
      historyFailed = true;
    } finally {
      historyLoading = false;
      historyLoaded = true;
    }
  }

  // loadMoreHistory pages backward from the oldest currently-loaded
  // row's own resolved_at -- the actual dimension GET /api/alerts/
  // history filters and orders by (internal/store/alerts.go's
  // AlertHistory), the same "before cursor, minus one so the inclusive
  // boundary isn't re-fetched" idiom Events.svelte's own loadMore uses.
  async function loadMoreHistory() {
    if (historyLoading || history.length === 0) return;
    historyLoading = true;
    historyFailed = false;
    try {
      const minResolved = Math.min(...history.map((h) => h.resolved_at));
      const older = await fetchAlertHistory({ to: minResolved - 1, limit: HISTORY_PAGE_LIMIT });
      history = [...history, ...older];
      historyHasMore = older.length === HISTORY_PAGE_LIMIT;
    } catch {
      historyFailed = true;
    } finally {
      historyLoading = false;
    }
  }

  // --- Rules (Task 11) ---------------------------------------------------

  let defaultsById = $state({});
  let editingId = $state(null);
  let addingNew = $state(false);
  let ruleSaveErrors = $state({});

  const NEW_RULE_TEMPLATE = {
    id: '',
    name: '',
    enabled: true,
    builtin: false,
    type: 'threshold',
    kind: 'host',
    entity_glob: '*',
    entity_class: '',
    metric: '',
    op: '>',
    threshold: 0,
    clear_threshold: 0,
    warn_threshold: 0,
    critical_threshold: 0,
    band_family: '',
    for_seconds: 300,
    clear_seconds: 300,
    event_kinds: '',
    min_severity: '',
    clear_event_kinds: '',
    clear_max_severity: '',
    severity: 'warning',
    channels: '',
    renotify_hours: 0,
    updated_at: 0,
  };

  async function saveRule(updated) {
    const exists = alertRules.list.some((r) => r.id === updated.id);
    const next = exists ? alertRules.list.map((r) => (r.id === updated.id ? updated : r)) : [...alertRules.list, updated];
    try {
      await alertRules.save(next);
      ruleSaveErrors = { ...ruleSaveErrors, [updated.id]: null };
      editingId = null;
      addingNew = false;
    } catch (err) {
      ruleSaveErrors = { ...ruleSaveErrors, [updated.id]: err instanceof Error ? err.message : String(err) };
    }
  }

  async function deleteRule(id) {
    ruleSaveErrors = { ...ruleSaveErrors, [id]: null };
    try {
      await alertRules.save(alertRules.list.filter((r) => r.id !== id));
    } catch (err) {
      // builtins can never reach here -- the delete control is never
      // rendered for one -- so a failure here means something else
      // changed server-side between load and click; surface it plainly.
      ruleSaveErrors = { ...ruleSaveErrors, [id]: err instanceof Error ? err.message : String(err) };
    }
  }

  async function resetRuleToDefault(id) {
    const def = defaultsById[id];
    if (!def) return;
    await saveRule(def);
  }

  onMount(() => {
    alertRules.ensureLoaded();
    loadSilences();
    loadHistoryFirstPage();
    fetchAlertRules({ defaults: true })
      .then((resp) => {
        defaultsById = Object.fromEntries(resp.rules.map((r) => [r.id, r]));
      })
      .catch(() => {
        // "Reset to default" simply won't offer a target value this
        // session; every other Rules feature is unaffected.
      });
  });
</script>

<div class="alerts-view">
  <h1 class="page-title">Alerts</h1>

  <div class="card alerts-view__channels">
    <span class="microlabel">Channels</span>
    {#if channelNames.length === 0}
      <p class="microlabel alerts-view__empty">No delivery channels configured.</p>
    {:else}
      <ul class="alerts-view__channel-list">
        {#each channelNames as name (name)}
          {@const ok = channels[name] === 'ok'}
          <li class="alerts-view__channel-row">
            <HealthDot status={ok ? 'good' : 'warning'} label={channelLabel(name)} />
            {#if !ok}<span class="alerts-view__channel-detail">{channels[name]}</span>{/if}
          </li>
        {/each}
      </ul>
    {/if}
  </div>

  <div class="card alerts-view__active">
    <span class="microlabel">Active</span>
    {#if truncated > 0}
      <p class="microlabel alerts-view__truncated">
        Showing {activeSorted.length} of {firingCount} firing alerts.
      </p>
    {/if}
    {#if activeSorted.length === 0}
      <p class="alerts-view__calm">Nothing is alerting.</p>
    {:else}
      <ul class="alerts-view__rows">
        {#each activeSorted as a (rowKey(a))}
          {@const covering = a.silenced ? coveringSilence(a.rule_id, a.entity) : null}
          {@const href = alertEntityHref(a.kind, a.entity)}
          <li class="alerts-view__row" class:alerts-view__row--silenced={a.silenced}>
            <HealthDot status={SEVERITY_STATUS[a.severity] ?? 'warning'} />
            <div class="alerts-view__row-body">
              <div class="alerts-view__row-head">
                <span class="alerts-view__row-rule">{a.rule_name}</span>
                {#if a.entity}
                  {#if href}
                    <a class="alerts-view__row-entity" {href}>{a.entity}</a>
                  {:else}
                    <span class="alerts-view__row-entity">{a.entity}</span>
                  {/if}
                {/if}
                <span class="microlabel alerts-view__row-duration">firing for {firingDuration(a.fired_at, nowSec)}</span>
              </div>
              {#if a.metric}
                <div class="alerts-view__row-value">
                  {formatMetricValue(a.metric, a.value)} vs threshold {formatMetricValue(a.metric, a.threshold)}
                </div>
              {:else if a.summary}
                <div class="alerts-view__row-value">{a.summary}</div>
              {/if}
              {#if a.silenced}
                <div class="alerts-view__row-silence-status">
                  <span class="microlabel">{covering ? silenceLabel(covering, nowSec) : 'Silenced'}</span>
                  {#if covering}
                    <button type="button" class="alerts-view__lift" onclick={() => liftSilence(covering.id)}>Lift</button>
                  {/if}
                </div>
              {/if}
              {#if a.insightAnnotation}
                <a class="alerts-view__row-annotation" href={a.insightAnnotation.href}>{a.insightAnnotation.text}</a>
              {/if}
            </div>
            {#if !a.silenced}
              <div class="alerts-view__silence-control">
                <button
                  type="button"
                  class="alerts-view__silence-btn"
                  aria-label="Silence {a.rule_name} on {a.entity || 'this alert'}"
                  onclick={() => (openSilenceMenuKey = openSilenceMenuKey === rowKey(a) ? null : rowKey(a))}
                >
                  Silence ▾
                </button>
                {#if openSilenceMenuKey === rowKey(a)}
                  <div class="segmented alerts-view__silence-menu" role="group" aria-label="Silence duration">
                    {#each SILENCE_PRESET_HOURS as p (p.hours)}
                      <button type="button" class="segmented__btn" onclick={() => silence(a, p.hours)}>{p.label}</button>
                    {/each}
                  </div>
                {/if}
              </div>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
    {#if silenceActionError}<p class="alerts-view__error">{silenceActionError}</p>{/if}
    {#if silencesError}<p class="microlabel alerts-view__error">Couldn't load silence details -- rows still show as silenced, just without the countdown.</p>{/if}
  </div>

  <div class="card alerts-view__history">
    <span class="microlabel">History</span>
    {#if historyFailed}
      <p class="microlabel alerts-view__error">Couldn't load history. Try again shortly.</p>
    {:else if historyLoaded && history.length === 0}
      <p class="alerts-view__calm">Nothing has resolved yet.</p>
    {:else}
      <ul class="alerts-view__rows">
        {#each history as h (h.id)}
          <li class="alerts-view__row alerts-view__row--history">
            <HealthDot status={SEVERITY_STATUS[h.severity] ?? 'warning'} />
            <div class="alerts-view__row-body">
              <div class="alerts-view__row-head">
                <span class="alerts-view__row-rule">{h.rule_id}</span>
                {#if h.entity}<span class="alerts-view__row-entity">{h.entity}</span>{/if}
              </div>
              <div class="alerts-view__row-value">
                {describeResolveReason(h.resolve_reason)} · fired for {firingDuration(h.fired_at, h.resolved_at)}
              </div>
            </div>
          </li>
        {/each}
      </ul>
      {#if historyLoading}
        <p class="microlabel">Loading…</p>
      {:else if historyHasMore}
        <button type="button" class="alerts-view__load-more" onclick={loadMoreHistory}>Load more</button>
      {/if}
    {/if}
  </div>

  <div class="card alerts-view__rules">
    <span class="microlabel">Rules</span>
    <ul class="alerts-view__rule-list">
      {#each alertRules.list as rule (rule.id)}
        <li class="alerts-view__rule-row" data-rule-id={rule.id}>
          {#if editingId === rule.id}
            <RuleEditor
              {rule}
              saving={alertRules.saving}
              serverError={ruleSaveErrors[rule.id]}
              onSave={saveRule}
              onCancel={() => (editingId = null)}
            />
          {:else}
            <div class="alerts-view__rule-summary">
              <label class="alerts-view__rule-toggle">
                <input
                  type="checkbox"
                  checked={rule.enabled}
                  onchange={(e) => saveRule({ ...rule, enabled: e.currentTarget.checked })}
                />
                <span class="sr-only">Enable {rule.name}</span>
              </label>
              <div class="alerts-view__rule-text">
                <span class="alerts-view__rule-name">
                  {rule.name}
                  {#if rule.builtin}<span class="alerts-view__builtin-badge">Builtin</span>{/if}
                </span>
                <span class="alerts-view__rule-desc">{describeRule(rule.id, rule)}</span>
              </div>
              <div class="alerts-view__rule-actions">
                <button type="button" onclick={() => (editingId = rule.id)}>Edit</button>
                {#if rule.builtin}
                  <button type="button" onclick={() => resetRuleToDefault(rule.id)} disabled={!defaultsById[rule.id]}>
                    Reset to default
                  </button>
                {:else}
                  <button type="button" onclick={() => deleteRule(rule.id)}>Delete</button>
                {/if}
              </div>
            </div>
            {#if ruleSaveErrors[rule.id]}<p class="alerts-view__error">{ruleSaveErrors[rule.id]}</p>{/if}
          {/if}
        </li>
      {/each}
    </ul>

    {#if addingNew}
      <RuleEditor
        rule={NEW_RULE_TEMPLATE}
        isNew
        saving={alertRules.saving}
        serverError={ruleSaveErrors['']}
        onSave={saveRule}
        onCancel={() => (addingNew = false)}
      />
    {:else}
      <button type="button" class="alerts-view__add-rule" onclick={() => (addingNew = true)}>Add rule</button>
    {/if}
  </div>
</div>

<style>
  .alerts-view {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }
  .alerts-view__channels,
  .alerts-view__active,
  .alerts-view__history,
  .alerts-view__rules {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
  }
  .alerts-view__channel-list {
    margin: 0;
    padding: 0;
    list-style: none;
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
  }
  .alerts-view__channel-row {
    display: flex;
    align-items: baseline;
    gap: 0.6rem;
    flex-wrap: wrap;
  }
  .alerts-view__channel-detail {
    font-size: 0.82rem;
    color: var(--ink-2);
  }
  .alerts-view__calm,
  .alerts-view__empty {
    margin: 0;
    color: var(--ink-2);
  }
  .alerts-view__truncated {
    margin: 0;
    color: var(--ink-2);
  }
  .alerts-view__rows {
    margin: 0;
    padding: 0;
    list-style: none;
    display: flex;
    flex-direction: column;
  }
  .alerts-view__row {
    display: flex;
    gap: 0.6rem;
    padding: 0.6rem 0;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 8%, transparent);
    align-items: flex-start;
    flex-wrap: wrap;
  }
  .alerts-view__row--silenced {
    opacity: 0.55;
  }
  .alerts-view__row-body {
    flex: 1;
    min-width: 12rem;
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
  }
  .alerts-view__row-head {
    display: flex;
    align-items: baseline;
    gap: 0.5rem;
    flex-wrap: wrap;
  }
  .alerts-view__row-rule {
    font-weight: 600;
    color: var(--ink);
  }
  .alerts-view__row-entity {
    color: var(--ink-2);
    font-size: 0.85rem;
  }
  a.alerts-view__row-entity {
    text-decoration: none;
  }
  a.alerts-view__row-entity:hover {
    text-decoration: underline;
  }
  .alerts-view__row-duration {
    margin-left: auto;
  }
  .alerts-view__row-value {
    font-size: 0.85rem;
    color: var(--ink-2);
  }
  .alerts-view__row-silence-status {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }
  /* insight annotation (Phase 5 Task 13): the alert says what broke,
     this line says why -- deliberately quieter than the row's own
     value line (italic, muted) so it reads as supporting context, not
     a second alarm competing with the real one. */
  .alerts-view__row-annotation {
    font-size: 0.8rem;
    font-style: italic;
    color: var(--ink-2);
    text-decoration: none;
  }
  .alerts-view__row-annotation:hover {
    text-decoration: underline;
  }
  .alerts-view__lift {
    min-height: 32px;
    padding: 0 0.6rem;
    border-radius: 6px;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: transparent;
    color: var(--ink);
    font-size: 0.75rem;
    cursor: pointer;
  }
  .alerts-view__silence-control {
    position: relative;
    flex-shrink: 0;
  }
  .alerts-view__silence-btn {
    min-height: 40px;
    padding: 0 0.75rem;
    border-radius: 6px;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: transparent;
    color: var(--ink);
    font-size: 0.8rem;
    cursor: pointer;
  }
  .alerts-view__silence-menu {
    position: absolute;
    right: 0;
    top: calc(100% + 0.3rem);
    z-index: 5;
    background: var(--surface);
    box-shadow: 0 4px 16px color-mix(in oklab, black 20%, transparent);
  }
  .alerts-view__error {
    margin: 0;
    font-size: 0.82rem;
    color: var(--status-critical);
  }
  .alerts-view__load-more {
    align-self: center;
    min-height: 40px;
    padding: 0 1.25rem;
    margin-top: 0.4rem;
    border-radius: 6px;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: transparent;
    color: var(--ink);
    font-size: 0.85rem;
    cursor: pointer;
  }

  .alerts-view__rule-list {
    margin: 0;
    padding: 0;
    list-style: none;
    display: flex;
    flex-direction: column;
  }
  .alerts-view__rule-row {
    padding: 0.5rem 0;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 8%, transparent);
  }
  .alerts-view__rule-summary {
    display: flex;
    align-items: flex-start;
    flex-wrap: wrap;
    gap: 0.6rem;
  }
  .alerts-view__rule-toggle input {
    width: 16px;
    height: 16px;
    margin-top: 0.2rem;
  }
  .alerts-view__rule-text {
    /* flex-basis 12rem (not 0): at narrow viewports (reproduced live at
       375px) a bare "flex:1" let this column get squeezed to near-zero
       BEFORE the row-actions column wrapped to its own line, and the
       builtin badge -- an inline-flex child with nowhere to wrap to --
       overflowed its own sliver of a column and visually overlapped
       the Edit/Reset buttons sitting right next to it. A sane minimum
       width means the row's own flex-wrap (just below) kicks in and
       drops actions to a new line instead, well before that squeeze. */
    flex: 1 1 12rem;
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
    min-width: 0;
  }
  .alerts-view__rule-name {
    font-weight: 600;
    color: var(--ink);
    display: inline-flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 0.5rem;
  }
  .alerts-view__builtin-badge {
    font-family: var(--font-mono);
    font-size: 0.65rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    padding: 0.1rem 0.4rem;
    border-radius: 999px;
    background: color-mix(in oklab, var(--ink) 8%, transparent);
    color: var(--ink-2);
  }
  .alerts-view__rule-desc {
    font-size: 0.85rem;
    color: var(--ink-2);
  }
  .alerts-view__rule-actions {
    display: flex;
    gap: 0.5rem;
    flex-wrap: wrap;
    flex-shrink: 0;
  }
  .alerts-view__rule-actions button {
    min-height: 40px;
    padding: 0 0.75rem;
    border-radius: 6px;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: transparent;
    color: var(--ink);
    font-size: 0.8rem;
    cursor: pointer;
  }
  .alerts-view__rule-actions button:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
  .alerts-view__add-rule {
    align-self: flex-start;
    min-height: 40px;
    padding: 0 1rem;
    border-radius: 6px;
    border: 1px solid var(--series-1);
    background: color-mix(in oklab, var(--series-1) 15%, transparent);
    color: var(--series-1);
    font-size: 0.85rem;
    font-weight: 500;
    cursor: pointer;
  }
  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
  }
</style>
