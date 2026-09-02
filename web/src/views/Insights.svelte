<!--
  Insights: Phase 5's own view -- active cross-container impact
  findings, their history, the seven-rule library's own tuning, and (as
  a `.segmented` MODE inside this same view, never a second nav item --
  see router.ts's own doc) the container interaction map.

  Active is live off the SSE frame's own insights.active block, the
  exact Alerts.svelte precedent -- no polling for the list itself.
  History/Rules are plain fetch/PUT, matching Alerts' own pattern.
  The evidence drawer (shared between the List cards and the Map's own
  edges) fetches GET /api/insights/{id} on demand -- the frame's own
  compact items never carry evidence, by design (Task 9's own frame
  contract), so opening a drawer is the one place this view fetches per
  click rather than reading the live frame.

  Map mode polls GET /api/insights/graph on the same 2s cadence the
  frame itself ticks (the frame carries no graph block of its own --
  Task 9's frame contract is active-findings-only) -- only while Map
  mode is actually selected, so List mode costs nothing extra.

  The drawer ALSO always renders the interaction map, for either an
  Active or a History row alike: "as of that insight's own instant"
  (insights.ts' own drawerMapAnchor -- now for Active, the clicked row's
  own fired_at for History), the clicked insight's own edge(s)
  emphasized and every OTHER insight concurrent at that exact instant
  drawn muted but present (InteractionMap's own focusInsightId). Unlike
  the standalone Map mode above, this is a ONE-TIME snapshot built
  client-side the moment the drawer opens (loadDrawerMap,
  insights.ts' own selectOverlappingInsights + buildInsightGraph over
  GET /api/insights' active rows union GET /api/insights/history?from=
  <anchor>'s resolved ones), never re-polled -- reading a moment from an
  hour ago should not shift under the user while the drawer is open, and
  a snapshot also means this needs no server endpoint of its own: the
  existing evidence-bearing GET routes already answer "what overlapped
  this instant" once filtered by the store's own started_at/resolved_at
  columns. Rendered `compact` (InteractionMap's own doc) so a busy moment
  scrolls inside its own capped box rather than ballooning the drawer.

  The drawer ALSO renders the incident's own time-series chart(s) --
  owner's own follow-up ask, "insight history should also provide a
  graph of the incident if possible": the map answers WHO, this answers
  HOW BAD AND WHEN. incidentChart.ts' own planIncidentCharts maps each of
  the seven compiled-in rules to its real victim/culprit metrics (read
  off internal/insight's own rules and collectors, not guessed -- see
  that file's own per-rule doc), loadDrawerCharts fetches them from the
  SAME GET /api/series every other historical chart in this app already
  uses (TimeChart's own xDomain/non-live mode, a one-time snapshot fetch
  exactly like the map above, never polled), and a rule whose victim and
  culprit signal don't share a unit gets two compact charts rather than
  one misleadingly shared y-axis (TimeChart is single-axis by design).
  "If possible" is the operative word: a pruned or never-recorded window
  degrades to a quiet one-line hint per chart (incidentChart.ts' own
  hasChartableData), never a blank chart or an error.

  D2 calm: the empty state is deliberately quiet and specific about
  which evidence tier is live (tier 1/proxy vs PSI) rather than a bare
  "nothing here" -- Scott should be able to tell AT A GLANCE whether
  the reboot to psi=1 would actually change anything right now.
-->
<script>
  import { onMount, untrack } from 'svelte';
  import { live } from '../lib/sse.svelte';
  import { fetchInsight, fetchInsights, fetchInsightHistory, fetchInsightRules, putInsightRules, dismissInsight, fetchInsightGraph, fetchSeries } from '../lib/api';
  import {
    sortActiveInsights,
    confidenceLabel,
    activeDuration,
    describeRule,
    formatEvidenceNumber,
    EVIDENCE_LABEL,
    drawerMapAnchor,
    selectOverlappingInsights,
    buildInsightGraph,
  } from '../lib/insights';
  import { incidentChartWindow, incidentBand, incidentMarkers, hasChartableData, planIncidentCharts } from '../lib/incidentChart';
  import { sumSeriesByMetric } from '../lib/metrics';
  import { fmtPct, fmtRate } from '../lib/format';
  import { eventHref } from '../lib/eventHref';
  import HealthDot from '../components/HealthDot.svelte';
  import InteractionMap from '../components/InteractionMap.svelte';
  import TimeChart from '../components/TimeChart.svelte';

  let { mode: initialMode = undefined } = $props();

  const HISTORY_PAGE_LIMIT = 25;
  const GRAPH_POLL_MS = 2000;
  // OVERLAP_HISTORY_FETCH_LIMIT: the drawer's own loadDrawerMap fetches
  // GET /api/insights/history?from=<anchor> for every resolved insight
  // that overlaps the drawer's anchor -- capped at the endpoint's own
  // maximum (api_insights.go's maxInsightHistoryLimit) rather than its
  // smaller default, since this is a targeted, one-shot query (not a
  // "load more" page a user paces out), and under-fetching here would
  // silently under-draw the picture. A system with more than 500
  // resolutions AFTER a clicked insight's own anchor is the one accepted
  // edge case this can miss -- the clicked insight's own row is always
  // unioned in separately regardless (see loadDrawerMap's own doc), so
  // its own edge never disappears even then.
  const OVERLAP_HISTORY_FETCH_LIMIT = 500;
  const EMPTY_GRAPH = { nodes: [], edges: [] };
  // CHART_FORMATTERS maps incidentChart.ts' own plain-string
  // ChartFormatter enum (pct/rate/plain) to the real format.ts function
  // TimeChart's formatValue prop wants -- kept as a lookup table here,
  // not on ChartPlan itself, so that module stays pure and trivially
  // comparable in its own tests (no closures to compare).
  const CHART_FORMATTERS = { pct: fmtPct, rate: fmtRate, plain: undefined };
  // DRAWER_CHART_HEIGHT: shorter than TimeChart's own 220px default --
  // the drawer already stacks the map above these, and up to two charts
  // per rule (see incidentChart.ts' own doc on when a rule needs both),
  // so a compact height here matters more than it does for
  // ContainerDetail's own always-one-at-a-time charts.
  const DRAWER_CHART_HEIGHT = 150;
  const DISMISS_PRESETS = [
    { label: '1d', days: 1 },
    { label: '7d', days: 7 },
    { label: '30d', days: 30 },
  ];

  // SEVERITY_STATUS mirrors Alerts.svelte's own identical mapping --
  // store.Event's three-slot severity vocabulary (info/warning/alert,
  // insight/rules.go's own Finding.Severity doc) onto HealthDot's
  // four-slot status vocabulary.
  const SEVERITY_STATUS = { info: 'good', warning: 'warning', alert: 'critical' };

  // --- Active (live off the frame) ---------------------------------------

  let activeRaw = $derived(live.frame?.insights?.active ?? []);
  let active = $derived(sortActiveInsights(activeRaw));
  let tier = $derived(live.frame?.insights?.tier ?? 'proxy');
  let suppressed = $derived(live.frame?.insights?.suppressed ?? 0);
  let statementsByID = $derived(Object.fromEntries(active.map((i) => [i.id, i.statement])));

  let nowSec = $state(Math.floor(Date.now() / 1000));
  onMount(() => {
    const t = setInterval(() => (nowSec = Math.floor(Date.now() / 1000)), 1000);
    return () => clearInterval(t);
  });

  // --- List/Map mode (Task 14: a mode inside this view, #/insights/map) --

  // userChoseMode: once the user (or a deep link) explicitly picks a
  // mode, stop auto-switching under them as findings come and go --
  // only the FIRST, unforced landing defaults to "map when something's
  // active, list otherwise" (the plan's own rule: "the picture is the
  // better first read when there is something to look at, and the
  // worse one when there isn't").
  // untrack: a deliberate ONE-TIME read of the route's own initial mode
  // param -- TopConsumers.svelte's own initialResource follows the
  // identical contract for the identical reason (a route param seeds
  // state at mount; it does not stay reactively bound afterward). The
  // $effect just below is what stays reactive to `active` going
  // forward -- mode's own $state seed intentionally does NOT read
  // active.length itself (it would capture whatever active happens to
  // be on the FIRST render, typically still [] before the live frame's
  // first tick has even arrived, and never reconsider it again).
  let userChoseMode = $state(untrack(() => initialMode === 'map'));
  let mode = $state(untrack(() => (initialMode === 'map' ? 'map' : 'list')));
  $effect(() => {
    if (userChoseMode) return;
    mode = active.length > 0 ? 'map' : 'list';
  });
  function setMode(next) {
    mode = next;
    userChoseMode = true;
    if (typeof window !== 'undefined') {
      window.location.hash = next === 'map' ? '#/insights/map' : '#/insights';
    }
  }

  // --- Map data -----------------------------------------------------------

  let graph = $state({ nodes: [], edges: [] });
  $effect(() => {
    if (mode !== 'map') return;
    let cancelled = false;
    async function poll() {
      try {
        const g = await fetchInsightGraph();
        if (!cancelled) graph = g;
      } catch {
        // leave graph as it is -- the next 2s poll tries again.
      }
    }
    poll();
    const interval = setInterval(poll, GRAPH_POLL_MS);
    return () => {
      cancelled = true;
      clearInterval(interval);
    };
  });

  // --- Evidence drawer (shared: List cards AND Map edges open the SAME
  //     drawer) -----------------------------------------------------------

  let drawerID = $state(null);
  let drawerData = $state(null);
  let drawerLoading = $state(false);
  let drawerError = $state(false);

  // drawerGraph/drawerStatementsById/drawerMapLoading: the drawer's own
  // embedded interaction map (this view's own top-of-file doc) --
  // populated once by loadDrawerMap below, independent of the standalone
  // Map mode's `graph` state above (which may not even be populated yet
  // if the drawer was opened from List mode, and live-polls on a cadence
  // this snapshot deliberately does not follow).
  let drawerGraph = $state(EMPTY_GRAPH);
  let drawerStatementsById = $state({});
  let drawerMapLoading = $state(false);

  // drawerCharts/drawerChartXDomain/drawerChartMarkers/drawerChartBand/
  // drawerChartsLoading: the drawer's own incident chart(s) (this view's
  // own top-of-file doc) -- populated once by loadDrawerCharts below,
  // fully independent of the map state above (a rule can chart even when
  // its own map side has nothing concurrent to show, and vice versa).
  // drawerCharts is ChartPlan[] with `lines` replaced by a resolved
  // `series` (TimeChart-ready {label, colorVar, points}) plus each
  // chart's own `hasData`.
  let drawerCharts = $state([]);
  let drawerChartXDomain = $state(undefined);
  let drawerChartMarkers = $state([]);
  let drawerChartBand = $state(undefined);
  let drawerChartsLoading = $state(false);

  async function openDrawer(id) {
    drawerID = id;
    drawerData = null;
    drawerError = false;
    drawerLoading = true;
    drawerGraph = EMPTY_GRAPH;
    drawerStatementsById = {};
    drawerMapLoading = true;
    drawerCharts = [];
    drawerChartsLoading = true;
    try {
      drawerData = await fetchInsight(id);
    } catch {
      drawerError = true;
    } finally {
      drawerLoading = false;
    }
    if (drawerData) {
      loadDrawerMap(drawerData);
      loadDrawerCharts(drawerData);
    } else {
      drawerMapLoading = false;
      drawerChartsLoading = false;
    }
  }

  // loadDrawerMap assembles the drawer's map data for the clicked insight
  // `inst`: drawerMapAnchor picks the single instant (now for an Active
  // insight, its own fired_at for a History one), then the pool tested
  // against that anchor is `inst` itself (always present, regardless of
  // either fetch below -- guarantees "if only the clicked insight was
  // active, the map legitimately shows just that culprit-to-victim pair"
  // even if a fetch below fails or truncates) union the CURRENTLY active
  // set WITH evidence (fetchInsights, not this view's own live-frame-
  // derived `active` above -- that trimmed copy never carries evidence,
  // Task 9's own frame contract, and a share_pct-less edge would silently
  // lose its own width signal) union every resolved insight whose OWN
  // resolution is at or after the anchor (fetchInsightHistory({from:
  // anchor}) -- see OVERLAP_HISTORY_FETCH_LIMIT's own doc for why this is
  // a fresh targeted fetch, deliberately NOT a re-use of the History
  // section's own paginated `history` array above, which is a "newest
  // resolution first" prefix that can legitimately stop short of an
  // older overlapping insight the user never scrolled/loaded down to --
  // reusing it here would silently under-draw the picture instead of
  // asking the API the one targeted question it can already answer
  // directly.
  //
  // A snapshot, deliberately: this runs once when the drawer opens, never
  // on a poll -- see this view's own top-of-file doc.
  async function loadDrawerMap(inst) {
    const anchor = drawerMapAnchor(inst, nowSec);
    const [activeRows, historyRows] = await Promise.all([
      fetchInsights()
        .then((r) => r.active)
        .catch(() => []),
      fetchInsightHistory({ from: anchor, limit: OVERLAP_HISTORY_FETCH_LIMIT }).catch(() => []),
    ]);
    if (drawerID !== inst.id) return; // the drawer moved on (closed, or a different row) while this was in flight
    const overlap = selectOverlappingInsights([inst, ...activeRows, ...historyRows], anchor);
    drawerGraph = buildInsightGraph(overlap);
    drawerStatementsById = Object.fromEntries(overlap.map((i) => [i.id, i.statement]));
    drawerMapLoading = false;
  }

  // loadDrawerCharts assembles the drawer's incident chart(s) for the
  // clicked insight `inst`: planIncidentCharts (incidentChart.ts) maps
  // its own rule_id to which real metrics to chart, resolving a disk's
  // slot to its current raw device name off the live frame's own
  // disk_meta (Storage.svelte's own seedDiskSlot performs the identical
  // join) and a GPU engine's victim entity off the live frame's own
  // currently-known gpu keys -- both are CURRENT, live-frame lookups
  // (there is no per-instance record of either on the stored row itself),
  // so an incident whose device or GPU is no longer present degrades to
  // simply omitting that one line, never a wrong guess.
  //
  // Every line across every returned chart is grouped by its own (kind,
  // entity) pair first, so a pair more than one line needs (e.g. a
  // shared culprit's io.read_bps+write_bps feeding just one line, or two
  // DIFFERENT rules' lines that happened to want the same container) is
  // still only fetched once. hasChartableData decides each chart's own
  // real-vs-fallback rendering straight off the RAW per-pair fetch
  // result, before sumSeriesByMetric ever folds multiple metrics into
  // one line's own points -- see that function's own doc for why an
  // empty result is never an error here, only a quiet "nothing to show."
  async function loadDrawerCharts(inst) {
    const xDomain = incidentChartWindow(inst, nowSec);
    const plans = planIncidentCharts(inst, {
      diskMeta: live.frame?.disk_meta ?? {},
      gpuEntities: Object.keys(live.frame?.gpu ?? {}),
    });

    const pairs = new Map(); // "<kind>|<entity>" -> {kind, entity, metrics: Set<string>}
    for (const plan of plans) {
      for (const line of plan.lines) {
        const key = `${line.kind}|${line.entity}`;
        if (!pairs.has(key)) pairs.set(key, { kind: line.kind, entity: line.entity, metrics: new Set() });
        for (const m of line.metrics) pairs.get(key).metrics.add(m);
      }
    }

    const settled = await Promise.all(
      [...pairs.entries()].map(([key, { kind, entity, metrics }]) =>
        fetchSeries({ kind, entity, metrics: [...metrics], from: xDomain[0], to: xDomain[1] })
          .then((results) => [key, results])
          .catch(() => [key, []]),
      ),
    );
    if (drawerID !== inst.id) return; // the drawer moved on while this was in flight

    const resultsByPair = new Map(settled);
    const hasDataByPair = new Map([...resultsByPair].map(([key, results]) => [key, hasChartableData(results)]));

    drawerCharts = plans.map((plan) => ({
      ...plan,
      hasData: plan.lines.some((line) => hasDataByPair.get(`${line.kind}|${line.entity}`)),
      series: plan.lines.map((line) => {
        const results = resultsByPair.get(`${line.kind}|${line.entity}`) ?? [];
        const byMetric = Object.fromEntries(results.map((r) => [r.metric, r.points]));
        return { label: line.label, colorVar: line.colorVar, points: sumSeriesByMetric(byMetric, line.metrics) };
      }),
    }));
    drawerChartXDomain = xDomain;
    drawerChartMarkers = incidentMarkers(inst);
    drawerChartBand = incidentBand(inst, nowSec);
    drawerChartsLoading = false;
  }

  function closeDrawer() {
    drawerID = null;
    drawerData = null;
    drawerError = false;
    drawerGraph = EMPTY_GRAPH;
    drawerStatementsById = {};
    drawerMapLoading = false;
    drawerCharts = [];
    drawerChartsLoading = false;
  }
  function handleWindowKeydown(e) {
    if (e.key === 'Escape' && drawerID !== null) closeDrawer();
  }

  // evidenceRows: every EvidenceDTO field the drawer's own bundle
  // actually populated, in a stable declared order -- an unused field
  // (not every rule/shape/confidence populates every one, insight.
  // Evidence's own doc) is simply absent from this list rather than
  // rendered as a misleading "0".
  const EVIDENCE_FIELD_ORDER = [
    'culprit_share_pct',
    'device_util_pct',
    'await_ms',
    'victim_stall_pct',
    'window_minutes',
    'iowait_pct',
    'host_cpu_pct',
    'engine_busy_pct',
    'baseline_pct',
    'spin_count',
    'spin_window_minutes',
  ];
  let evidenceRows = $derived.by(() => {
    if (!drawerData?.evidence) return [];
    const ev = drawerData.evidence;
    return EVIDENCE_FIELD_ORDER.filter((k) => ev[k] !== undefined && ev[k] !== 0).map((k) => ({
      key: k,
      label: EVIDENCE_LABEL[k] ?? k,
      text: formatEvidenceNumber(k, ev[k]),
    }));
  });
  let evidenceOtherUsers = $derived(drawerData?.evidence?.other_users ?? []);

  // --- Dismiss --------------------------------------------------------------

  let dismissMenuOpenID = $state(null);
  let dismissError = $state(null);

  async function dismiss(id, days) {
    dismissError = null;
    try {
      await dismissInsight(id, days);
      dismissMenuOpenID = null;
      if (drawerID === id) closeDrawer();
      loadHistoryFirstPage();
    } catch (err) {
      dismissError = err instanceof Error ? err.message : String(err);
    }
  }

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
      const rows = await fetchInsightHistory({ limit: HISTORY_PAGE_LIMIT });
      history = rows;
      historyHasMore = rows.length === HISTORY_PAGE_LIMIT;
    } catch {
      historyFailed = true;
    } finally {
      historyLoading = false;
      historyLoaded = true;
    }
  }

  async function loadMoreHistory() {
    if (historyLoading || history.length === 0) return;
    historyLoading = true;
    historyFailed = false;
    try {
      const minResolved = Math.min(...history.map((h) => h.resolved_at));
      const older = await fetchInsightHistory({ to: minResolved - 1, limit: HISTORY_PAGE_LIMIT });
      history = [...history, ...older];
      historyHasMore = older.length === HISTORY_PAGE_LIMIT;
    } catch {
      historyFailed = true;
    } finally {
      historyLoading = false;
    }
  }

  // --- Rules ------------------------------------------------------------

  let rules = $state([]);
  let rulesLoading = $state(false);
  let rulesFailed = $state(false);
  let editingRuleID = $state(null);
  let draftOverrides = $state({});
  let ruleSaveErrors = $state({});

  async function loadRules() {
    rulesLoading = true;
    rulesFailed = false;
    try {
      const resp = await fetchInsightRules();
      rules = resp.rules;
    } catch {
      rulesFailed = true;
    } finally {
      rulesLoading = false;
    }
  }

  // effectiveOverrides reconstructs the RAW overrides map a rule
  // currently carries (as opposed to its effective, defaults-merged
  // thresholds) by keeping only the keys that differ from that same
  // rule's own compiled-in default -- InsightRuleDTO exposes the
  // merged result, not the raw override set, so this is how an
  // UNCHANGED rule resubmits exactly what it already had rather than
  // pinning every threshold as an explicit override on its next save.
  function effectiveOverrides(rule) {
    const out = {};
    for (const [k, v] of Object.entries(rule.thresholds ?? {})) {
      if (v !== rule.defaults?.[k]) out[k] = v;
    }
    return out;
  }

  async function saveRule(updatedFields, rule) {
    const merged = { ...rule, ...updatedFields };
    const body = rules.map((r) =>
      r.rule_id === rule.rule_id
        ? { rule_id: r.rule_id, enabled: merged.enabled, notify: merged.notify, overrides: merged.overrides ?? effectiveOverrides(r) }
        : { rule_id: r.rule_id, enabled: r.enabled, notify: r.notify, overrides: effectiveOverrides(r) },
    );
    try {
      const resp = await putInsightRules(body);
      rules = resp.rules;
      ruleSaveErrors = { ...ruleSaveErrors, [rule.rule_id]: null };
    } catch (err) {
      ruleSaveErrors = { ...ruleSaveErrors, [rule.rule_id]: err instanceof Error ? err.message : String(err) };
    }
  }

  function startEditThresholds(rule) {
    editingRuleID = rule.rule_id;
    draftOverrides = { ...rule.thresholds };
  }
  function cancelEditThresholds() {
    editingRuleID = null;
  }
  async function saveThresholds(rule) {
    await saveRule({ overrides: { ...draftOverrides } }, rule);
    editingRuleID = null;
  }
  function resetThresholdsDraft(rule) {
    draftOverrides = { ...rule.defaults };
  }

  onMount(() => {
    loadHistoryFirstPage();
    loadRules();
  });
</script>

<svelte:window onkeydown={handleWindowKeydown} />

<div class="insights-view">
  <div class="insights-view__head">
    <h1 class="page-title">Insights</h1>
    <div class="segmented" role="group" aria-label="View">
      <button type="button" class="segmented__btn" class:segmented__btn--active={mode === 'list'} onclick={() => setMode('list')}>
        List
      </button>
      <button type="button" class="segmented__btn" class:segmented__btn--active={mode === 'map'} onclick={() => setMode('map')}>
        Map
      </button>
    </div>
  </div>

  {#if suppressed > 0}
    <p class="microlabel insights-view__suppressed">{suppressed} insight{suppressed === 1 ? '' : 's'} suppressed by the active-findings cap.</p>
  {/if}

  {#if mode === 'map'}
    <div class="card insights-view__map">
      <InteractionMap {graph} statementsById={statementsByID} {tier} onOpenDrawer={openDrawer} />
    </div>
  {:else}
    <div class="card insights-view__active">
      <span class="microlabel">Active</span>
      {#if active.length === 0}
        <div class="insights-view__calm">
          <p class="insights-view__calm-line">
            {tier === 'psi' ? 'Nothing is contending right now.' : 'Nothing is contending right now — the fleet is coexisting peacefully.'}
          </p>
          {#if tier === 'proxy'}
            <p class="microlabel insights-view__tier-note">
              Running at tier 1 (proxy evidence). PSI is off, so a victim can't yet be named individually — enabling
              <code>psi=1</code> would let findings name who's actually stalled, and for how long.
            </p>
          {/if}
        </div>
      {:else}
        <ul class="insights-view__rows">
          {#each active as i (i.id)}
            {@const victimHref = eventHref(i.victim_kind, i.victim)}
            {@const culpritName = i.culprit || i.culprits?.split(',')[0]}
            {@const culpritHref = culpritName ? eventHref('container', culpritName) : null}
            <li class="insights-view__row">
              <HealthDot status={SEVERITY_STATUS[i.severity] ?? 'warning'} />
              <div class="insights-view__row-body">
                <button type="button" class="insights-view__statement-btn" onclick={() => openDrawer(i.id)}>
                  {i.statement}
                </button>
                <div class="insights-view__row-meta">
                  <span class="insights-view__chip insights-view__chip--{i.confidence}">{confidenceLabel(i.confidence)}</span>
                  {#if i.victim}
                    {#if victimHref}<a href={victimHref}>{i.victim}</a>{:else}<span>{i.victim}</span>{/if}
                  {/if}
                  {#if culpritName}
                    <span class="insights-view__row-arrow">←</span>
                    {#if culpritHref}<a href={culpritHref}>{i.culprits || i.culprit}</a>{:else}<span>{i.culprits || i.culprit}</span>{/if}
                  {/if}
                  <span class="microlabel insights-view__row-duration">active for {activeDuration(i.started_at, nowSec)}</span>
                </div>
              </div>
              <div class="insights-view__dismiss-control">
                <button
                  type="button"
                  class="insights-view__dismiss-btn"
                  aria-label="Not useful: dismiss this insight"
                  onclick={() => (dismissMenuOpenID = dismissMenuOpenID === i.id ? null : i.id)}
                >
                  Not useful ▾
                </button>
                {#if dismissMenuOpenID === i.id}
                  <div class="segmented insights-view__dismiss-menu" role="group" aria-label="Dismiss duration">
                    {#each DISMISS_PRESETS as p (p.days)}
                      <button type="button" class="segmented__btn" onclick={() => dismiss(i.id, p.days)}>{p.label}</button>
                    {/each}
                  </div>
                {/if}
              </div>
            </li>
          {/each}
        </ul>
      {/if}
      {#if dismissError}<p class="insights-view__error">{dismissError}</p>{/if}
    </div>
  {/if}

  <div class="card insights-view__history">
    <span class="microlabel">History</span>
    {#if historyFailed}
      <p class="microlabel insights-view__error">Couldn't load history. Try again shortly.</p>
    {:else if historyLoaded && history.length === 0}
      <p class="insights-view__calm-line">Nothing has resolved yet.</p>
    {:else}
      <ul class="insights-view__rows">
        {#each history as h (h.id)}
          <li class="insights-view__row insights-view__row--history">
            <HealthDot status={SEVERITY_STATUS[h.severity] ?? 'warning'} />
            <div class="insights-view__row-body">
              <button type="button" class="insights-view__statement-btn" onclick={() => openDrawer(h.id)}>{h.statement}</button>
              <div class="insights-view__row-meta">
                <span class="insights-view__chip insights-view__chip--{h.confidence}">{confidenceLabel(h.confidence)}</span>
                <span class="microlabel">{h.resolve_reason} · lasted {activeDuration(h.started_at, h.resolved_at)}</span>
              </div>
            </div>
          </li>
        {/each}
      </ul>
      {#if historyLoading}
        <p class="microlabel">Loading…</p>
      {:else if historyHasMore}
        <button type="button" class="insights-view__load-more" onclick={loadMoreHistory}>Load more</button>
      {/if}
    {/if}
  </div>

  <div class="card insights-view__rules">
    <span class="microlabel">Rules</span>
    {#if rulesFailed}
      <p class="microlabel insights-view__error">Couldn't load rules.</p>
    {:else}
      <ul class="insights-view__rule-list">
        {#each rules as rule (rule.rule_id)}
          <li class="insights-view__rule-row">
            <div class="insights-view__rule-summary">
              <label class="insights-view__rule-toggle">
                <input type="checkbox" checked={rule.enabled} onchange={(e) => saveRule({ enabled: e.currentTarget.checked }, rule)} />
                <span class="sr-only">Enable {rule.title}</span>
              </label>
              <div class="insights-view__rule-text">
                <span class="insights-view__rule-name">
                  {rule.title}
                  <span class="insights-view__tier-badge">{rule.tier === 'psi' ? 'PSI' : 'tier 1'}</span>
                </span>
                <span class="insights-view__rule-desc">{describeRule(rule.rule_id, rule.thresholds, rule.title)}</span>
              </div>
              <div class="insights-view__rule-actions">
                <label class="insights-view__notify-toggle" title="Only for confirmed findings">
                  <input type="checkbox" checked={rule.notify} onchange={(e) => saveRule({ notify: e.currentTarget.checked }, rule)} />
                  Notify <span class="microlabel">(confirmed only)</span>
                </label>
                <button type="button" onclick={() => startEditThresholds(rule)}>Thresholds</button>
              </div>
            </div>
            {#if editingRuleID === rule.rule_id}
              <div class="insights-view__threshold-editor">
                {#each Object.keys(rule.thresholds) as key (key)}
                  <label class="insights-view__threshold-field">
                    <span class="microlabel">{key}</span>
                    <input
                      type="number"
                      step="any"
                      value={draftOverrides[key]}
                      oninput={(e) => (draftOverrides = { ...draftOverrides, [key]: Number(e.currentTarget.value) })}
                    />
                  </label>
                {/each}
                <div class="insights-view__threshold-actions">
                  <button type="button" onclick={() => resetThresholdsDraft(rule)}>Reset to default</button>
                  <button type="button" onclick={cancelEditThresholds}>Cancel</button>
                  <button type="button" onclick={() => saveThresholds(rule)}>Save</button>
                </div>
              </div>
            {/if}
            {#if ruleSaveErrors[rule.rule_id]}<p class="insights-view__error">{ruleSaveErrors[rule.rule_id]}</p>{/if}
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</div>

{#if drawerID !== null}
  <div class="insights-drawer__overlay" onclick={(e) => e.target === e.currentTarget && closeDrawer()} role="presentation">
    <div class="insights-drawer card" role="dialog" aria-modal="true" aria-labelledby="insights-drawer-title">
      <div class="insights-drawer__head">
        <h2 id="insights-drawer-title" class="insights-drawer__title">Evidence</h2>
        <button type="button" class="insights-drawer__close" onclick={closeDrawer} aria-label="Close">&times;</button>
      </div>
      {#if drawerLoading}
        <p class="microlabel">Loading…</p>
      {:else if drawerError}
        <p class="insights-view__error">Couldn't load this insight's evidence.</p>
      {:else if drawerData}
        <p class="insights-drawer__statement">{drawerData.statement}</p>
        <div class="insights-drawer__facts">
          <span class="insights-view__chip insights-view__chip--{drawerData.confidence}">{confidenceLabel(drawerData.confidence)}</span>
          <span class="microlabel">{drawerData.tier === 'psi' ? 'PSI tier' : 'tier 1 (proxy)'}</span>
          <span class="microlabel">{drawerData.state === 'active' ? `active for ${activeDuration(drawerData.started_at, nowSec)}` : `resolved (${drawerData.resolve_reason})`}</span>
        </div>
        <div class="insights-drawer__map">
          <span class="microlabel">Interaction map</span>
          {#if drawerMapLoading}
            <p class="microlabel">Loading…</p>
          {:else}
            <InteractionMap
              graph={drawerGraph}
              statementsById={drawerStatementsById}
              tier={drawerData.tier}
              onOpenDrawer={openDrawer}
              focusInsightId={drawerData.id}
              compact={true}
            />
          {/if}
        </div>
        {#if drawerChartsLoading || drawerCharts.length > 0}
          <div class="insights-drawer__charts">
            <span class="microlabel">Incident timeline</span>
            {#if drawerChartsLoading}
              <p class="microlabel">Loading…</p>
            {:else}
              {#each drawerCharts as chart (chart.key)}
                <div class="insights-drawer__chart">
                  <span class="microlabel insights-drawer__chart-title">{chart.title}</span>
                  {#if chart.hasData}
                    <TimeChart
                      series={chart.series}
                      formatValue={CHART_FORMATTERS[chart.formatter]}
                      markers={drawerChartMarkers}
                      band={drawerChartBand}
                      xDomain={drawerChartXDomain}
                      height={DRAWER_CHART_HEIGHT}
                    />
                  {:else}
                    <p class="microlabel insights-drawer__chart-empty">History for this window isn't available.</p>
                  {/if}
                </div>
              {/each}
            {/if}
          </div>
        {/if}
        {#if evidenceRows.length > 0}
          <dl class="insights-drawer__evidence">
            {#each evidenceRows as row (row.key)}
              <dt>{row.label}</dt>
              <dd class="tabular-nums">{row.text}</dd>
            {/each}
          </dl>
        {/if}
        {#if evidenceOtherUsers.length > 0}
          <p class="microlabel insights-drawer__other-users">Also touching this resource: {evidenceOtherUsers.join(', ')}</p>
        {/if}
        <div class="insights-drawer__dismiss">
          <span class="microlabel">Not useful?</span>
          {#each DISMISS_PRESETS as p (p.days)}
            <button type="button" onclick={() => dismiss(drawerData.id, p.days)}>{p.label}</button>
          {/each}
        </div>
      {/if}
    </div>
  </div>
{/if}

<style>
  .insights-view {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }
  .insights-view__head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.75rem;
    flex-wrap: wrap;
  }
  .insights-view__suppressed {
    margin: 0;
  }
  .insights-view__map,
  .insights-view__active,
  .insights-view__history,
  .insights-view__rules {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
  }
  .insights-view__calm {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
    padding: 1rem 0;
  }
  .insights-view__calm-line {
    margin: 0;
    color: var(--ink-2);
  }
  .insights-view__tier-note {
    margin: 0;
  }
  .insights-view__tier-note code {
    font-family: var(--font-mono);
  }
  .insights-view__rows {
    margin: 0;
    padding: 0;
    list-style: none;
    display: flex;
    flex-direction: column;
  }
  .insights-view__row {
    display: flex;
    gap: 0.6rem;
    padding: 0.6rem 0;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 8%, transparent);
    align-items: flex-start;
    flex-wrap: wrap;
  }
  .insights-view__row-body {
    flex: 1;
    min-width: 12rem;
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
  }
  .insights-view__statement-btn {
    text-align: left;
    border: none;
    background: none;
    padding: 0;
    font: inherit;
    font-weight: 500;
    color: var(--ink);
    cursor: pointer;
  }
  .insights-view__statement-btn:hover {
    text-decoration: underline;
  }
  .insights-view__row-meta {
    display: flex;
    align-items: baseline;
    gap: 0.5rem;
    flex-wrap: wrap;
    font-size: 0.82rem;
    color: var(--ink-2);
  }
  .insights-view__row-arrow {
    color: var(--ink-3);
  }
  .insights-view__row-duration {
    margin-left: auto;
  }
  .insights-view__chip {
    flex-shrink: 0;
    font-family: var(--font-mono);
    font-size: 0.62rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    padding: 0.1rem 0.4rem;
    border-radius: 999px;
    border: 1px solid currentColor;
  }
  .insights-view__chip--likely {
    color: var(--ink-2);
  }
  .insights-view__chip--confirmed {
    color: var(--status-warning);
  }
  .insights-view__dismiss-control {
    position: relative;
    flex-shrink: 0;
  }
  .insights-view__dismiss-btn {
    min-height: 40px;
    padding: 0 0.75rem;
    border-radius: 6px;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: transparent;
    color: var(--ink);
    font-size: 0.8rem;
    cursor: pointer;
  }
  .insights-view__dismiss-menu {
    position: absolute;
    right: 0;
    top: calc(100% + 0.3rem);
    z-index: 5;
    background: var(--surface);
    box-shadow: 0 4px 16px color-mix(in oklab, black 20%, transparent);
  }
  .insights-view__error {
    margin: 0;
    font-size: 0.82rem;
    color: var(--status-critical);
  }
  .insights-view__load-more {
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

  .insights-view__rule-list {
    margin: 0;
    padding: 0;
    list-style: none;
    display: flex;
    flex-direction: column;
  }
  .insights-view__rule-row {
    padding: 0.5rem 0;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 8%, transparent);
  }
  .insights-view__rule-summary {
    display: flex;
    align-items: flex-start;
    flex-wrap: wrap;
    gap: 0.6rem;
  }
  .insights-view__rule-toggle input {
    width: 16px;
    height: 16px;
    margin-top: 0.2rem;
  }
  .insights-view__rule-text {
    flex: 1 1 12rem;
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
    min-width: 0;
  }
  .insights-view__rule-name {
    font-weight: 600;
    color: var(--ink);
    display: inline-flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 0.5rem;
  }
  .insights-view__tier-badge {
    font-family: var(--font-mono);
    font-size: 0.62rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    padding: 0.1rem 0.4rem;
    border-radius: 999px;
    background: color-mix(in oklab, var(--ink) 8%, transparent);
    color: var(--ink-2);
  }
  .insights-view__rule-desc {
    font-size: 0.85rem;
    color: var(--ink-2);
  }
  .insights-view__rule-actions {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    flex-wrap: wrap;
    flex-shrink: 0;
  }
  .insights-view__notify-toggle {
    display: inline-flex;
    align-items: center;
    gap: 0.3rem;
    font-size: 0.8rem;
    color: var(--ink);
  }
  .insights-view__rule-actions button {
    min-height: 40px;
    padding: 0 0.75rem;
    border-radius: 6px;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: transparent;
    color: var(--ink);
    font-size: 0.8rem;
    cursor: pointer;
  }
  .insights-view__threshold-editor {
    margin-top: 0.6rem;
    padding: 0.75rem;
    display: flex;
    flex-wrap: wrap;
    gap: 0.75rem;
    border-radius: 8px;
    background: color-mix(in oklab, var(--ink) 3%, transparent);
  }
  .insights-view__threshold-field {
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
    width: 9rem;
  }
  .insights-view__threshold-field input {
    min-height: 36px;
  }
  .insights-view__threshold-actions {
    display: flex;
    align-items: flex-end;
    gap: 0.5rem;
    flex-wrap: wrap;
  }
  .insights-view__threshold-actions button {
    min-height: 36px;
    padding: 0 0.75rem;
    border-radius: 6px;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: transparent;
    color: var(--ink);
    font-size: 0.8rem;
    cursor: pointer;
  }

  .insights-drawer__overlay {
    position: fixed;
    inset: 0;
    z-index: 20;
    background: color-mix(in oklab, black 45%, transparent);
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 1rem;
  }
  .insights-drawer {
    width: 100%;
    max-width: 30rem;
    max-height: calc(100vh - 2rem);
    padding: 1.25rem;
    display: flex;
    flex-direction: column;
    gap: 0.65rem;
    overflow-y: auto;
  }
  .insights-drawer__head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 0.5rem;
  }
  .insights-drawer__title {
    margin: 0;
    font-family: var(--font-display);
    font-weight: 700;
    font-size: 1.1rem;
    color: var(--ink);
  }
  .insights-drawer__close {
    width: 32px;
    height: 32px;
    flex-shrink: 0;
    border: 0;
    color: var(--ink-2);
    background: transparent;
    font-size: 1.1rem;
    cursor: pointer;
  }
  .insights-drawer__statement {
    margin: 0;
    font-size: 0.95rem;
    color: var(--ink);
  }
  .insights-drawer__facts {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    flex-wrap: wrap;
  }
  .insights-drawer__map {
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
  }
  .insights-drawer__charts {
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
  }
  .insights-drawer__chart {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
  }
  .insights-drawer__chart-title {
    color: var(--ink-2);
  }
  .insights-drawer__chart-empty {
    margin: 0;
    padding: 0.75rem 0;
  }
  .insights-drawer__evidence {
    margin: 0;
    display: grid;
    grid-template-columns: 1fr auto;
    gap: 0.35rem 1rem;
  }
  .insights-drawer__evidence dt {
    color: var(--ink-2);
    font-size: 0.82rem;
  }
  .insights-drawer__evidence dd {
    margin: 0;
    font-size: 0.85rem;
    color: var(--ink);
    text-align: right;
  }
  .insights-drawer__other-users {
    margin: 0;
  }
  .insights-drawer__dismiss {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    flex-wrap: wrap;
    padding-top: 0.5rem;
    border-top: 1px solid color-mix(in oklab, var(--ink) 8%, transparent);
  }
  .insights-drawer__dismiss button {
    min-height: 36px;
    padding: 0 0.7rem;
    border-radius: 6px;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: transparent;
    color: var(--ink);
    font-size: 0.8rem;
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
