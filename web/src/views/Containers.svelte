<!--
  Containers: the fleet table. Desktop (>=768px) gets a dense, sortable,
  filterable table; mobile gets a card list instead (same data, fewer
  columns). See the sorting $effect below for the SORT STABILITY
  contract: rows must not reorder just because a value ticked.
-->
<script>
  import { onMount, untrack } from 'svelte';
  import { live } from '../lib/sse.svelte';
  import { matchesContainerFilter, sortContainerNames } from '../lib/containersSort';
  import { containerHealthStatus, partitionContainerNames } from '../lib/containerStatus';
  import { fmtBytes, fmtPct, fmtRate } from '../lib/format';
  import { seriesPointsToRing } from '../lib/livering';
  import { fetchContainers, fetchSeries } from '../lib/api';
  import { buildCompareHash } from '../lib/compareRoute';
  import { composeGroups } from '../lib/composeGroups';
  import { activityInputFor, fleetActivity } from '../lib/fleetActivity';
  import { resourceScaleMax } from '../lib/topFromFrame';
  import { groups } from '../lib/groups.svelte';
  import ContainerIcon from '../components/ContainerIcon.svelte';
  import HealthDot from '../components/HealthDot.svelte';
  import ContainerRow from '../components/ContainerRow.svelte';

  let { initialState = 'all' } = $props();
  const STATE_FILTERS = [
    { key: 'all', label: 'All' },
    { key: 'running', label: 'Running' },
    { key: 'active', label: 'Active now' },
    { key: 'stopped', label: 'Stopped' },
    { key: 'attention', label: 'Needs attention' },
  ];
  let stateFilter = $derived(STATE_FILTERS.some((f) => f.key === initialState) ? initialState : 'all');

  const LIVE_WINDOW_SEC = 900;
  // MAX_CONCURRENT_SEED_FETCHES caps how many /api/series requests this
  // view fires at once while seeding every row's CPU sparkline --
  // 23+ containers all firing their own history fetch simultaneously on
  // mount would be a needless request burst for data that only ever
  // backs a 60px-wide sparkline; a small worker pool spreads them out
  // instead, without making any one row wait on every other's turn.
  const MAX_CONCURRENT_SEED_FETCHES = 6;

  // ariaName covers the one column (health) whose visible label is
  // deliberately empty (just a dot column) -- a sort button with no
  // visible text and no aria-label has NO accessible name at all until
  // it becomes the active column (sortIndicator only renders once
  // active), which a screen reader user would hit on every page load.
  //
  // width (additive -- column-width jitter fix, Scott: "when values
  // change, the width of the columns change size... this happens
  // constantly and is not good looking") feeds the <colgroup> below,
  // which is what makes table-layout:fixed's per-column widths real:
  // fixed layout sizes columns from the FIRST row it sees (a colgroup's
  // own <col> widths, when present) rather than from every row's actual
  // content on every render, which is what let a value's string length
  // changing ("17.2 KB/s" -> "947.6 B/s") recompute the whole table's
  // column widths under the PREVIOUS auto layout. Sized for each
  // column's own worst-case rendered text (see ContainerRow's per-cell
  // formatting) with some slack, not the current narrowest content.
  // image is the one column left undefined: table-layout:fixed gives
  // any column with no explicit width whatever's left over from
  // width:100% once every other column's own width is subtracted, which
  // is exactly the "flexible, takes the remaining slack" the ask wants
  // for the one column whose content (image tags) varies the most.
  const COLUMNS = [
    // select (multi detail view): the compare checkbox, quiet and
    // unsortable -- leftmost, ahead of the health dot, so it reads as
    // "pick which rows" before "here's their status," matching the
    // left-to-right order a floating compare bar's own "Compare N ->"
    // action implies.
    { key: 'select', label: '', ariaName: 'Compare', sortable: false, width: '1.75rem' },
    { key: 'health', label: 'ST', ariaName: 'Health', sortable: true, width: '2.25rem' },
    { key: 'name', label: 'Name', sortable: true, width: '10.5rem' },
    { key: 'cpu', label: 'CPU', sortable: true, numeric: true, width: '18.5rem' }, // icon+sparkline(220px, flex-shrink:0)+text -- matches ContainerRow's own cpu-cell sizing
    { key: 'mem', label: 'Mem', sortable: true, numeric: true, width: '9rem' }, // e.g. "888.8 MiB (88.8%)"
    { key: 'net', label: 'Net', sortable: true, numeric: true, width: '7.5rem' }, // stacked "↓ 888.8 KB/s" / "↑ 888.8 KB/s"
    { key: 'io', label: 'IO', sortable: true, numeric: true, width: '7.5rem' }, // stacked "r 888.8 KB/s" / "w 888.8 KB/s"
    { key: 'gpu', label: 'GPU', sortable: true, numeric: true, width: '4rem' },
    { key: 'pids', label: 'PIDs', sortable: true, numeric: true, width: '3.5rem' },
    { key: 'uptime', label: 'Uptime', sortable: true, numeric: true, width: '6rem' }, // e.g. "365d 23h"
    { key: 'image', label: 'Image', sortable: true },
  ];

  // NOT_RUNNING_COLUMNS mirrors COLUMNS but widens the health column
  // enough for showState's own state-word label ("restarting" is the
  // longest realistic one) -- the running table never passes showState,
  // so it keeps the tighter icon-only width.
  const NOT_RUNNING_COLUMNS = COLUMNS.map((col) => (col.key === 'health' ? { ...col, width: '5rem' } : col));

  // seedContainerCpuRings fetches each running container's own last-15-
  // minutes cpu.pct history and hands it to that row via onSeeded(name,
  // points) as each result lands -- a small worker-pool pattern: `limit`
  // workers each pull the next name off a shared cursor until the list
  // is exhausted, so at most `limit` fetches are ever in flight
  // together. A single row's failed fetch just leaves that row unseeded
  // (its sparkline builds up live-only, same as before this feature)
  // rather than aborting the rest of the pool.
  async function seedContainerCpuRings(names, signal, onSeeded) {
    const to = Math.floor(Date.now() / 1000);
    const from = to - LIVE_WINDOW_SEC;
    let cursor = 0;
    async function worker() {
      for (;;) {
        const i = cursor++;
        if (i >= names.length) return;
        const name = names[i];
        try {
          const results = await fetchSeries({ kind: 'container', entity: name, metrics: ['cpu.pct'], from, to, signal });
          onSeeded(name, seriesPointsToRing(results[0]?.points ?? []));
        } catch (err) {
          if (err?.name === 'AbortError') return; // the whole pool was torn down (unmount) -- stop, don't keep pulling names
        }
      }
    }
    const poolSize = Math.min(MAX_CONCURRENT_SEED_FETCHES, names.length);
    await Promise.all(Array.from({ length: poolSize }, worker));
  }

  // seedTargets: name -> the callback that hands THAT row's own ring its
  // seed once fetched. Deliberately a plain Map, not $state -- see
  // registerSeedTarget's own doc for why threading this payload through
  // reactive state instead (one shared object, updated once per landed
  // fetch) doesn't work here.
  const seedTargets = new Map();

  // registerSeedTarget is how a ContainerRow opts into this view's
  // seeding: it registers its own onSeed callback once, on mount, and
  // this view calls straight into it later when that row's own fetch
  // lands -- entirely outside Svelte's reactivity, a plain imperative
  // handoff. An earlier version of this threaded seed points through one
  // shared `$state` object instead (one entry added per landed fetch,
  // read by each row as `seedPointsByName[name]`) -- correct, but
  // wasteful at this scale: every row's own prop-dependent effect
  // re-evaluated on EVERY one of the 23 updates to that shared object,
  // not just the update that actually touched its own key (~500 re-runs
  // total instead of 23). A plain Map + direct callback call has no
  // shared reactive dependency for 23 concurrent writes to contend
  // over: each row's registration effect depends only on this stable
  // function reference (never reassigned), so it runs exactly once, and
  // delivering a seed later touches no Svelte state at all until the
  // row's OWN ring writes to it.
  function registerSeedTarget(name, onSeed) {
    seedTargets.set(name, onSeed);
    return () => seedTargets.delete(name);
  }

  onMount(() => {
    groups.ensureLoaded();
  });

  onMount(() => {
    const controller = new AbortController();
    fetchContainers()
      .then((containers) => {
        const names = containers.filter((c) => c.state === 'running').map((c) => c.name);
        return seedContainerCpuRings(names, controller.signal, (name, points) => {
          seedTargets.get(name)?.(points); // no target: row was filtered out or never mounted -- nothing to seed
        });
      })
      .catch((err) => {
        if (err?.name === 'AbortError') return; // unmounted before the inventory fetch resolved
        // A failed inventory fetch just means no rows get seeded this
        // visit -- every sparkline still builds up live-only, same as
        // before this feature.
      });
    return () => controller.abort();
  });

  let filterText = $state('');
  let sortColumn = $state('cpu');
  let sortDir = $state('desc');
  let stoppedExpanded = $state(false);

  // selectedNames: the compare checkbox's own selection, a plain Set
  // reassigned (not mutated in place) on every toggle -- Svelte 5 runes
  // need a fresh reference to notice the change, same convention
  // TopConsumers' own hiddenHeroIdx Set already uses. Persists across a
  // running<->stopped toggle and a live re-sort alike (it's keyed by
  // name, independent of either), and is NOT reset by a filter-text
  // change -- a container filtered out of view stays selected, so
  // narrowing the filter to check one more teammate doesn't silently
  // drop the ones already picked.
  let selectedNames = $state(new Set());

  function toggleSelected(name) {
    const next = new Set(selectedNames);
    if (next.has(name)) next.delete(name);
    else next.add(name);
    selectedNames = next;
  }

  function clearSelection() {
    selectedNames = new Set();
  }

  let selectedCount = $derived(selectedNames.size);
  let compareHref = $derived(buildCompareHash([...selectedNames]));

  // composeGroupsList: docker-compose stacks with >=2 currently-known
  // members -- the Groups chip row's own data. Recomputes on every live
  // frame (composeGroups is cheap: one pass over the container map), but
  // its OUTPUT only actually changes when project membership itself
  // changes, not on ordinary metric ticks.
  let composeGroupsList = $derived(composeGroups(live.frame?.containers ?? {}));

  // Custom group management: editingGroupName names the ONE custom-group
  // chip (if any) currently swapped for its inline rename/delete editor
  // -- at most one at a time, plain view-local state (not persisted,
  // not shared with groups.svelte's own store).
  let editingGroupName = $state(null);
  let editingNameInput = $state('');

  function openEditGroup(name) {
    editingGroupName = name;
    editingNameInput = name;
  }
  function cancelEditGroup() {
    editingGroupName = null;
  }
  async function submitRenameGroup(oldName) {
    const newName = editingNameInput.trim();
    if (newName && newName !== oldName) await groups.rename(oldName, newName);
    editingGroupName = null;
  }
  async function deleteGroup(name) {
    await groups.remove(name);
    editingGroupName = null;
  }

  // nameSetKey is a $derived string that only actually CHANGES value when
  // the container name SET changes (add/remove) -- it recomputes every
  // tick internally, but Svelte 5's push-pull reactivity means a
  // downstream $effect reading it only re-fires when the VALUE differs,
  // which is the whole trick this sort-stability contract relies on.
  let nameSetKey = $derived(
    Object.keys(live.frame?.containers ?? {})
      .sort()
      .join('|'),
  );
  let sortedNames = $state([]);

  $effect(() => {
    // Re-sort ONLY on a header click (sortColumn/sortDir change) or when
    // the container name SET changes (nameSetKey's value changes) -- NOT
    // on every live frame, so rows don't reorder as CPU/mem/etc. tick.
    // sortColumn/sortDir/nameSetKey are read directly (registering as
    // this effect's dependencies); live.frame is read through untrack so
    // ITS OWN per-tick change can't also retrigger this effect -- only
    // the metric VALUES it carries change every tick, and those must
    // never drive a re-sort on their own.
    sortColumn;
    sortDir;
    nameSetKey;
    const names = nameSetKey ? nameSetKey.split('|') : [];
    const frame = untrack(() => live.frame);
    sortedNames = sortContainerNames(names, frame?.containers ?? {}, sortColumn, sortDir, frame?.ts ?? 0);
  });

  function setSort(column) {
    if (sortColumn === column) {
      sortDir = sortDir === 'asc' ? 'desc' : 'asc';
    } else {
      sortColumn = column;
      sortDir = column === 'name' || column === 'image' ? 'asc' : 'desc';
    }
  }

  function sortIndicator(column) {
    if (sortColumn !== column) return '';
    return sortDir === 'asc' ? ' ▲' : ' ▼';
  }

  // sortAriaLabel gives every sort button a real accessible name
  // regardless of visible label text (the health column has none) and
  // announces the CURRENT sort state the same way the visible ▲/▼
  // indicator does for sighted users -- a screen reader user gets that
  // information too, not just "button".
  function sortAriaLabel(col) {
    const name = col.ariaName ?? col.label;
    if (sortColumn !== col.key) return `Sort by ${name}`;
    return `Sort by ${name}, currently sorted ${sortDir === 'asc' ? 'ascending' : 'descending'}`;
  }

  // hostMemBytes backs the "Active now" filter's memory reading for a
  // container with no limit of its own -- the frame carries the host
  // total only implicitly (see resourceScaleMax).
  let hostMemBytes = $derived(resourceScaleMax('mem', live.frame));

  // filteredNames re-derives every frame (it reads live.frame for each
  // name's current image/state) but never reorders anything -- it only
  // narrows sortedNames' already-stable order down to what the filter
  // box and running/stopped split allow through.
  //
  // "Active now" is lib/fleetActivity.ts's own rule, not a local cpu.pct
  // test: the fleet strip's glowing blocks link HERE, so the count in
  // its summary line and the list this filter produces have to be the
  // same containers. That rule is CPU plus memory, network, disk IO and
  // GPU now (the any-metric glow pass), so a container saturating a disk
  // while idling its CPU shows up in both or neither.
  let filteredNames = $derived(
    sortedNames.filter((name) => {
      const c = live.frame?.containers?.[name];
      if (!c || !matchesContainerFilter(name, c.image ?? '', filterText)) return false;
      if (stateFilter === 'running') return c.state === 'running';
      if (stateFilter === 'active') return c.state === 'running' && fleetActivity(activityInputFor(c.metrics, hostMemBytes)).active;
      if (stateFilter === 'stopped') return c.state !== 'running';
      if (stateFilter === 'attention') return c.state === 'running' && containerHealthStatus(c.state, c.health) !== 'good';
      return true;
    }),
  );
  // Never-started ("created") containers -- ephemeral CI-runner spawns
  // are the live example that prompted this split -- get their own
  // bucket rather than folding into "stopped": both land in the same
  // collapsed "Not running" section below (still high-churn, still
  // nothing live to show), but keeping them distinct lets each row say
  // which it actually is.
  let containerPartition = $derived(partitionContainerNames(filteredNames, live.frame?.containers ?? {}));
  let runningNames = $derived(containerPartition.running);
  let stoppedNames = $derived(containerPartition.stopped);
  let createdNames = $derived(containerPartition.created);
  let notRunningNames = $derived([...stoppedNames, ...createdNames]);
  let notRunningOpen = $derived(stateFilter === 'stopped' || stoppedExpanded);
</script>

<div class="containers-view">
  <h1 class="page-title">Containers</h1>

  {#if composeGroupsList.length > 0 || groups.list.length > 0}
    <!-- Groups: one chip per docker-compose project with >=2 currently-
         known members, PLUS one per saved custom group (Scott's own
         ask: "make a way for a user to group certain containers
         together for easy compare") -- "I have multiple containers
         that work together as a team for an app," surfaced up front
         rather than relying on the fleet happening to share a naming
         pattern the filter box can search for. Clicking a chip jumps
         straight into compare, pre-filled with that group's own members
         (compareRoute.ts's buildCompareHash already sorts them,
         matching a compose chip's own canonical member order -- a
         custom group's member order is whatever it was saved with).
         Custom chips carry a small bookmark glyph (subtle distinction
         from a derived compose chip, Scott's own suggestion) plus a
         trailing ⋯ that swaps the chip for a tiny rename/delete editor
         -- deleting a group never touches the containers it named. -->
    <div class="containers-view__groups" role="group" aria-label="Groups">
      <span class="microlabel containers-view__groups-label">Groups</span>
      {#each composeGroupsList as group (group.project)}
        <a class="containers-view__group-chip" href={buildCompareHash(group.names)}>
          {group.project}
          <span class="containers-view__group-count">×{group.names.length}</span>
        </a>
      {/each}
      {#each groups.list as g (g.name)}
        {#if editingGroupName === g.name}
          <form
            class="containers-view__group-chip containers-view__group-chip--editing"
            onsubmit={(e) => {
              e.preventDefault();
              submitRenameGroup(g.name);
            }}
          >
            <input
              type="text"
              class="containers-view__group-edit-input"
              bind:value={editingNameInput}
              aria-label={`Rename group ${g.name}`}
              onkeydown={(e) => {
                if (e.key === 'Escape') cancelEditGroup();
              }}
            />
            <button type="submit" class="containers-view__group-edit-btn" aria-label="Save name" title="Save">&check;</button>
            <button
              type="button"
              class="containers-view__group-edit-btn"
              onclick={() => deleteGroup(g.name)}
              aria-label={`Delete group ${g.name}`}
              title="Delete"
            >
              &#128465;
            </button>
            <button type="button" class="containers-view__group-edit-btn" onclick={cancelEditGroup} aria-label="Cancel" title="Cancel"
              >&times;</button
            >
          </form>
        {:else}
          <span class="containers-view__group-chip containers-view__group-chip--custom">
            <a class="containers-view__group-chip-link" href={buildCompareHash(g.members)}>
              <span class="containers-view__group-bookmark" aria-hidden="true">&#128278;</span>
              {g.name}
              <span class="containers-view__group-count">×{g.members.length}</span>
            </a>
            <button
              type="button"
              class="containers-view__group-manage"
              onclick={() => openEditGroup(g.name)}
              aria-label={`Manage group ${g.name}`}
            >
              &#8943;
            </button>
          </span>
        {/if}
      {/each}
    </div>
  {/if}

  <div class="containers-view__filters">
    <div class="containers-view__state-filters" aria-label="Container status filters">
      {#each STATE_FILTERS as filter (filter.key)}
        <a
          class="containers-view__state-filter"
          class:containers-view__state-filter--active={stateFilter === filter.key}
          href={filter.key === 'all' ? '#/containers' : `#/containers?state=${filter.key}`}
          aria-current={stateFilter === filter.key ? 'page' : undefined}
        >{filter.label}</a>
      {/each}
    </div>
    <input
      type="text"
      class="containers-view__filter"
      placeholder="Filter by name or image…"
      bind:value={filterText}
      aria-label="Filter containers by name or image"
    />
  </div>

  {#if runningNames.length === 0 && notRunningNames.length === 0}
    <p class="microlabel containers-view__empty">
            {filterText || stateFilter !== 'all' ? 'No containers match these filters.' : 'No containers yet.'}
    </p>
  {:else}
    <!-- Desktop: dense sortable table, its own horizontal-scroll
         container so a narrow-but->=768px viewport scrolls the table,
         never the page. -->
    <div class="card containers-view__table-wrap hidden md:block">
      <table class="containers-table">
        <colgroup>
          {#each COLUMNS as col (col.key)}
            <col style={col.width ? `width: ${col.width}` : undefined} />
          {/each}
        </colgroup>
        <thead>
          <tr>
            {#each COLUMNS as col (col.key)}
              <th class:containers-table__th--numeric={col.numeric}>
                {#if col.sortable}
                  <button
                    type="button"
                    class="containers-table__sort-btn"
                    onclick={() => setSort(col.key)}
                    aria-label={sortAriaLabel(col)}
                  >
                    <span class="microlabel" aria-hidden="true">{col.label}{sortIndicator(col.key)}</span>
                  </button>
                {:else}
                  <!-- select: no sort exists for "is this row checked," so
                       this is a plain, non-interactive (and, since it's
                       empty, non-visually-meaningful) header cell -- unlike
                       the icon-only sort BUTTONS this ariaName/sortAriaLabel
                       machinery exists for, a bare <th> with no control in
                       it needs no accessible name of its own; each row's
                       own checkbox already carries its own aria-label. -->
                  <span class="microlabel" aria-hidden="true">{col.label}</span>
                {/if}
              </th>
            {/each}
          </tr>
        </thead>
        <tbody>
          {#each runningNames as name (name)}
            <ContainerRow {name} {registerSeedTarget} selected={selectedNames.has(name)} onToggleSelect={() => toggleSelected(name)} />
          {/each}
        </tbody>
      </table>
    </div>

    <!-- Mobile: rail rows instead of table rows, one shared card (same
         "list lives in one card, hairline between rows" convention as
         the disk list/event feed) rather than a stack of same-shape
         per-container cards. The compare checkbox sits OUTSIDE the name/
         stats link (a sibling, not a nested interactive element) so
         checking it doesn't also trigger the link's own navigation. -->
    <div class="card containers-view__cards flex md:hidden">
      {#each runningNames as name (name)}
        {@const c = live.frame?.containers?.[name]}
        {#if c}
          <div class="containers-view__card">
            <input
              type="checkbox"
              class="container-row__select"
              checked={selectedNames.has(name)}
              onchange={() => toggleSelected(name)}
              aria-label={`Compare ${name}`}
            />
            <a class="containers-view__card-link" href={`#/containers/${encodeURIComponent(name)}`}>
              <div class="containers-view__card-head">
                <HealthDot status={containerHealthStatus(c.state, c.health)} />
                <ContainerIcon {name} icon={c.icon} size={20} />
                <span class="containers-view__card-name">{name}</span>
                {#if c.compose_project}
                  <span class="containers-view__card-compose-tag">{c.compose_project}</span>
                {/if}
              </div>
              <div class="containers-view__card-stats">
                <span class="tabular-nums">CPU {fmtPct(c.metrics['cpu.pct'] ?? 0)}</span>
                <span class="tabular-nums">Mem {fmtBytes(c.metrics['mem.bytes'] ?? 0)}</span>
                <span class="tabular-nums">
                  Net &darr;{fmtRate(c.metrics['net.rx_bps'] ?? 0)} &uarr;{fmtRate(c.metrics['net.tx_bps'] ?? 0)}
                </span>
              </div>
            </a>
          </div>
        {/if}
      {/each}
    </div>

    {#if notRunningNames.length > 0}
      <div class="containers-view__stopped-header">
        {#if stateFilter === 'stopped'}
          <span class="containers-view__stopped-label microlabel">Not running ({notRunningNames.length})</span>
        {:else}
          <button type="button" class="containers-view__stopped-toggle" onclick={() => (stoppedExpanded = !stoppedExpanded)}>
          <span class="microlabel">
            {notRunningOpen ? '▼' : '▶'} Not running ({notRunningNames.length})
          </span>
          </button>
        {/if}
      </div>
      {#if notRunningOpen}
        <!-- Stopped (exited/dead/etc.) and created (never-started) share
             this one collapsed section -- both are "nothing live to show"
             -- but each row still names its own real state (showState
             below), since a health dot's color alone isn't enough to
             tell the two apart (see HealthDot's own doc). -->
        <div class="card containers-view__table-wrap hidden md:block">
          <table class="containers-table">
            <colgroup>
              {#each NOT_RUNNING_COLUMNS as col (col.key)}
                <col style={col.width ? `width: ${col.width}` : undefined} />
              {/each}
            </colgroup>
            <tbody>
              {#each notRunningNames as name (name)}
                <ContainerRow {name} {registerSeedTarget} showState selected={selectedNames.has(name)} onToggleSelect={() => toggleSelected(name)} />
              {/each}
            </tbody>
          </table>
        </div>
        <div class="card containers-view__cards flex md:hidden">
          {#each notRunningNames as name (name)}
            {@const c = live.frame?.containers?.[name]}
            {#if c}
              <div class="containers-view__card">
                <input
                  type="checkbox"
                  class="container-row__select"
                  checked={selectedNames.has(name)}
                  onchange={() => toggleSelected(name)}
                  aria-label={`Compare ${name}`}
                />
                <a class="containers-view__card-link" href={`#/containers/${encodeURIComponent(name)}`}>
                  <div class="containers-view__card-head">
                    <HealthDot status={containerHealthStatus(c.state, c.health)} />
                    <ContainerIcon {name} icon={c.icon} size={20} />
                    <span class="containers-view__card-name">{name}</span>
                    {#if c.compose_project}
                      <span class="containers-view__card-compose-tag">{c.compose_project}</span>
                    {/if}
                  </div>
                  <div class="containers-view__card-stats">
                    <span>{c.state}</span>
                  </div>
                </a>
              </div>
            {/if}
          {/each}
        </div>
      {/if}
    {/if}
  {/if}

  {#if selectedCount >= 2}
    <!-- Floating compare bar: appears once >=2 rows are checked (in
         either section, or the mobile card lists above), regardless of
         current filter/sort/scroll position -- dismissing clears the
         selection outright (equivalent to unchecking everything) rather
         than merely hiding an already-decided bar, so a later re-check
         starts from a clean slate instead of a bar silently reappearing
         mid-task. -->
    <div class="containers-view__compare-bar">
      <span class="containers-view__compare-count">{selectedCount} selected</span>
      <a class="containers-view__compare-btn" href={compareHref}>Compare {selectedCount} &rarr;</a>
      <button
        type="button"
        class="containers-view__compare-dismiss"
        onclick={clearSelection}
        aria-label="Clear selection"
      >
        ✕
      </button>
    </div>
  {/if}
</div>

<style>
  .containers-view {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }
  .containers-view__filter {
    min-height: 40px;
    padding: 0 0.75rem;
    border-radius: 6px;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: var(--surface);
    color: var(--ink);
    font-size: 0.9rem;
    max-width: 24rem;
  }
  .containers-view__filters {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.75rem;
    flex-wrap: wrap;
  }
  .containers-view__state-filters {
    display: inline-flex;
    flex-wrap: wrap;
    gap: 0.25rem;
    padding: 0.2rem;
    border: 1px solid var(--border);
    border-radius: 10px;
    background: var(--surface-soft);
  }
  .containers-view__state-filter {
    min-height: 32px;
    display: inline-flex;
    align-items: center;
    padding: 0 0.65rem;
    border-radius: 7px;
    color: var(--ink-2);
    font-size: 0.75rem;
    font-weight: 550;
    text-decoration: none;
  }
  .containers-view__state-filter:hover {
    color: var(--ink);
  }
  .containers-view__state-filter--active {
    background: var(--surface);
    color: var(--accent-strong);
    box-shadow: 0 1px 3px color-mix(in oklab, var(--ink) 12%, transparent);
  }
  .containers-view__empty {
    margin: 0;
  }
  .containers-view__table-wrap {
    overflow-x: auto;
    padding: 0;
  }
  /* table-layout:fixed (column-width jitter fix) sizes every column ONCE,
     from the <colgroup> above -- never recomputed from a row's own
     content -- so a value's string length changing on live tick can't
     nudge any column's width, the CONSTANT churn Scott flagged ("this
     happens constantly and is not good looking"). min-width/width still
     set the table's own overall floor/fill exactly as before; the fix
     is entirely in HOW the space inside that gets divided among columns. */
  .containers-table {
    width: 100%;
    border-collapse: collapse;
    min-width: 76.75rem; /* +1.75rem, the new leading select column's own width */
    table-layout: fixed;
  }
  .containers-table thead th {
    padding: 0.5rem 0.6rem;
    text-align: left;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 12%, transparent);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .containers-table__th--numeric {
    text-align: right;
  }
  .containers-table__sort-btn {
    background: transparent;
    border: none;
    cursor: pointer;
    padding: 0.2rem 0;
    min-height: 40px;
    display: flex;
    align-items: center;
  }
  .containers-table__th--numeric .containers-table__sort-btn {
    width: 100%;
    justify-content: flex-end;
  }
  .containers-view__stopped-header {
    margin-top: 0.25rem;
  }
  .containers-view__stopped-toggle {
    background: transparent;
    border: none;
    cursor: pointer;
    padding: 0.4rem 0;
    min-height: 40px;
  }
  .containers-view__stopped-label {
    display: inline-block;
    padding: 0.7rem 0;
  }
  .containers-view__cards {
    flex-direction: column;
    padding: 0;
  }
  /* Rail row, not a card -- was one .card per container (up to 20+ of
     them stacked); a hairline between rows instead, matching the disk
     list/event feed convention. A row, not just a link, since the
     compare checkbox (container-row__select below) is now a sibling of
     the name/stats link rather than nested inside it -- an <input>
     nested in an <a> would fire the link's own navigation on every
     checkbox click too. */
  .containers-view__card {
    padding: 0.75rem 1rem;
    display: flex;
    align-items: flex-start;
    gap: 0.6rem;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 8%, transparent);
  }
  .containers-view__card:last-child {
    border-bottom: none;
  }
  .containers-view__card input.container-row__select {
    margin-top: 0.2rem; /* nudges the checkbox down to the name row's own baseline, not the card's top edge */
  }
  .containers-view__card-link {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
    text-decoration: none;
    color: var(--ink);
  }
  .containers-view__card-head {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    flex-wrap: wrap;
  }
  .containers-view__card-name {
    font-weight: 500;
  }
  .containers-view__card-compose-tag {
    color: var(--ink-2);
    font-family: var(--font-mono);
    font-size: 0.7rem;
  }
  .containers-view__card-stats {
    display: flex;
    flex-wrap: wrap;
    gap: 0.75rem;
    font-size: 0.8rem;
    color: var(--ink-2);
    font-family: var(--font-mono);
  }
  /* Same rule as ContainerRow.svelte's own .container-row__select --
     duplicated rather than shared, since Svelte scopes each component's
     <style> block independently and this class is used here (the mobile
     card's own checkbox) as well as there (the desktop table's). */
  .container-row__select {
    accent-color: var(--series-1);
    cursor: pointer;
  }

  /* Groups: a quiet chip row, same visual language as the Metrics page's
     own hero-chart legend chips (colored pill, monospace) minus the
     color -- a compose project isn't one of this app's 8 categorical
     series slots, just a label, so these stay a neutral tint rather than
     borrowing a --series-N hue that would misleadingly suggest a
     specific chart-line identity before compare has even been opened. */
  .containers-view__groups {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 0.4rem;
  }
  .containers-view__groups-label {
    margin-right: 0.15rem;
  }
  .containers-view__group-chip {
    display: inline-flex;
    align-items: center;
    gap: 0.3rem;
    padding: 0.25rem 0.6rem;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    border-radius: 999px;
    background: color-mix(in oklab, var(--ink) 5%, transparent);
    color: var(--ink);
    text-decoration: none;
    font-family: var(--font-mono);
    font-size: 0.75rem;
  }
  .containers-view__group-chip:hover {
    background: color-mix(in oklab, var(--ink) 10%, transparent);
  }
  .containers-view__group-count {
    color: var(--ink-2);
  }

  /* Custom groups (Scott's own ask): same pill, but it's now a <span>
     wrapping a link PLUS a manage button rather than a bare <a> -- the
     hover/background/border above still apply unchanged; only the
     trailing edge tightens to make room for the manage button, the
     same asymmetric-padding trick compare__chip uses for its own
     trailing remove-x. The bookmark glyph is the one subtle visual
     tell apart from a derived compose chip (no color, no bolder
     border -- still a quiet chip, just not a plain one). */
  .containers-view__group-chip--custom {
    padding: 0.25rem 0.4rem 0.25rem 0.6rem;
  }
  .containers-view__group-chip-link {
    display: inline-flex;
    align-items: center;
    gap: 0.3rem;
    color: inherit;
    text-decoration: none;
  }
  .containers-view__group-bookmark {
    font-size: 0.7rem;
    opacity: 0.8;
  }
  .containers-view__group-manage {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    border-radius: 50%;
    border: none;
    background: transparent;
    color: var(--ink-2);
    cursor: pointer;
    font-size: 0.7rem;
    flex-shrink: 0;
  }
  .containers-view__group-manage:hover {
    background: color-mix(in oklab, var(--ink) 12%, transparent);
    color: var(--ink);
  }

  /* Editing: the same chip swaps its link+manage-button content for a
     tiny inline rename/delete form -- still one pill, not a popover,
     keeping this light (no floating-panel positioning to get wrong). */
  .containers-view__group-chip--editing {
    padding: 0.2rem 0.4rem;
    gap: 0.35rem;
  }
  .containers-view__group-edit-input {
    min-height: 22px;
    padding: 0 0.4rem;
    border-radius: 4px;
    border: 1px solid color-mix(in oklab, var(--ink) 20%, transparent);
    background: var(--surface);
    color: var(--ink);
    font-family: var(--font-mono);
    font-size: 0.75rem;
    width: 8rem;
  }
  .containers-view__group-edit-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    border-radius: 50%;
    border: none;
    background: transparent;
    color: var(--ink-2);
    cursor: pointer;
    font-size: 0.75rem;
    flex-shrink: 0;
  }
  .containers-view__group-edit-btn:hover {
    background: color-mix(in oklab, var(--ink) 12%, transparent);
    color: var(--ink);
  }

  /* Floating compare bar: fixed to the viewport (not the page flow), so
     it stays reachable regardless of scroll position or which section
     (running/stopped, desktop table/mobile cards) the 2nd checkbox was
     actually ticked in. Centered at the bottom, full-width-minus-margin
     on a narrow viewport rather than a fixed pixel width that could
     overflow it. */
  .containers-view__compare-bar {
    position: fixed;
    left: 50%;
    /* Clears the fixed mobile TabBar (56px, same clearance Layout.svelte's
       own .layout__content reserves) plus a little air -- without this
       the two fixed-bottom elements overlap directly, reproduced live at
       375px. Desktop (>=768px, no TabBar) reverts to a plain 1.25rem. */
    bottom: calc(56px + 0.75rem);
    transform: translateX(-50%);
    z-index: 11;
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 0.6rem 0.6rem 0.6rem 1rem;
    border-radius: 999px;
    background: var(--surface);
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    box-shadow: 0 4px 16px color-mix(in oklab, var(--ink) 20%, transparent);
    max-width: calc(100vw - 2rem);
  }
  @media (min-width: 48rem) {
    .containers-view__compare-bar {
      bottom: 1.25rem;
    }
  }
  .containers-view__compare-count {
    font-family: var(--font-mono);
    font-size: 0.8rem;
    color: var(--ink-2);
    white-space: nowrap;
  }
  /* Tinted-pill treatment, matching every other "active/primary" control
     in this app (.segmented__btn--active, the Metrics hero's own legend
     chips) rather than a one-off solid-fill button -- a bold custom
     color combination has no other precedent anywhere in this codebase. */
  .containers-view__compare-btn {
    padding: 0.5rem 1rem;
    border-radius: 999px;
    border: 1px solid color-mix(in oklab, var(--series-1) 45%, transparent);
    background: color-mix(in oklab, var(--series-1) 18%, transparent);
    color: var(--series-1);
    text-decoration: none;
    font-weight: 600;
    font-size: 0.85rem;
    white-space: nowrap;
  }
  .containers-view__compare-dismiss {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    border-radius: 50%;
    border: none;
    background: transparent;
    color: var(--ink-2);
    cursor: pointer;
    flex-shrink: 0;
  }
  .containers-view__compare-dismiss:hover {
    background: color-mix(in oklab, var(--ink) 10%, transparent);
    color: var(--ink);
  }
</style>
