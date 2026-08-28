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
  import { containerHealthStatus } from '../lib/containerStatus';
  import { fmtBytes, fmtPct, fmtRate } from '../lib/format';
  import { seriesPointsToRing } from '../lib/livering';
  import { fetchContainers, fetchSeries } from '../lib/api';
  import ContainerIcon from '../components/ContainerIcon.svelte';
  import HealthDot from '../components/HealthDot.svelte';
  import ContainerRow from '../components/ContainerRow.svelte';

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
  const COLUMNS = [
    { key: 'health', label: '', ariaName: 'Health', sortable: true },
    { key: 'name', label: 'Name', sortable: true },
    { key: 'cpu', label: 'CPU', sortable: true, numeric: true },
    { key: 'mem', label: 'Mem', sortable: true, numeric: true },
    { key: 'net', label: 'Net', sortable: true, numeric: true },
    { key: 'io', label: 'IO', sortable: true, numeric: true },
    { key: 'gpu', label: 'GPU', sortable: true, numeric: true },
    { key: 'pids', label: 'PIDs', sortable: true, numeric: true },
    { key: 'uptime', label: 'Uptime', sortable: true, numeric: true },
    { key: 'image', label: 'Image', sortable: true },
  ];

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

  // filteredNames re-derives every frame (it reads live.frame for each
  // name's current image/state) but never reorders anything -- it only
  // narrows sortedNames' already-stable order down to what the filter
  // box and running/stopped split allow through.
  let filteredNames = $derived(
    sortedNames.filter((name) => {
      const c = live.frame?.containers?.[name];
      return c ? matchesContainerFilter(name, c.image ?? '', filterText) : false;
    }),
  );
  let runningNames = $derived(filteredNames.filter((name) => live.frame?.containers?.[name]?.state === 'running'));
  let stoppedNames = $derived(filteredNames.filter((name) => live.frame?.containers?.[name]?.state !== 'running'));
</script>

<div class="containers-view">
  <h1 class="page-title">Containers</h1>

  <input
    type="text"
    class="containers-view__filter"
    placeholder="Filter by name or image…"
    bind:value={filterText}
    aria-label="Filter containers by name or image"
  />

  {#if runningNames.length === 0 && stoppedNames.length === 0}
    <p class="microlabel containers-view__empty">
      {filterText ? 'No containers match that filter.' : 'No containers yet.'}
    </p>
  {:else}
    <!-- Desktop: dense sortable table, its own horizontal-scroll
         container so a narrow-but->=768px viewport scrolls the table,
         never the page. -->
    <div class="card containers-view__table-wrap hidden md:block">
      <table class="containers-table">
        <thead>
          <tr>
            {#each COLUMNS as col (col.key)}
              <th class:containers-table__th--numeric={col.numeric}>
                <button
                  type="button"
                  class="containers-table__sort-btn"
                  onclick={() => setSort(col.key)}
                  aria-label={sortAriaLabel(col)}
                >
                  <span class="microlabel" aria-hidden="true">{col.label}{sortIndicator(col.key)}</span>
                </button>
              </th>
            {/each}
          </tr>
        </thead>
        <tbody>
          {#each runningNames as name (name)}
            <ContainerRow {name} {registerSeedTarget} />
          {/each}
        </tbody>
      </table>
    </div>

    <!-- Mobile: rail rows instead of table rows, one shared card (same
         "list lives in one card, hairline between rows" convention as
         the disk list/event feed) rather than a stack of same-shape
         per-container cards. -->
    <div class="card containers-view__cards flex md:hidden">
      {#each runningNames as name (name)}
        {@const c = live.frame?.containers?.[name]}
        {#if c}
          <a class="containers-view__card" href={`#/containers/${encodeURIComponent(name)}`}>
            <div class="containers-view__card-head">
              <HealthDot status={containerHealthStatus(c.state, c.health)} />
              <ContainerIcon {name} icon={c.icon} size={20} />
              <span class="containers-view__card-name">{name}</span>
            </div>
            <div class="containers-view__card-stats">
              <span class="tabular-nums">CPU {fmtPct(c.metrics['cpu.pct'] ?? 0)}</span>
              <span class="tabular-nums">Mem {fmtBytes(c.metrics['mem.bytes'] ?? 0)}</span>
              <span class="tabular-nums">
                Net &darr;{fmtRate(c.metrics['net.rx_bps'] ?? 0)} &uarr;{fmtRate(c.metrics['net.tx_bps'] ?? 0)}
              </span>
            </div>
          </a>
        {/if}
      {/each}
    </div>

    {#if stoppedNames.length > 0}
      <div class="containers-view__stopped-header">
        <button type="button" class="containers-view__stopped-toggle" onclick={() => (stoppedExpanded = !stoppedExpanded)}>
          <span class="microlabel">
            {stoppedExpanded ? '▼' : '▶'} Stopped ({stoppedNames.length})
          </span>
        </button>
      </div>
      {#if stoppedExpanded}
        <div class="card containers-view__table-wrap hidden md:block">
          <table class="containers-table">
            <tbody>
              {#each stoppedNames as name (name)}
                <ContainerRow {name} {registerSeedTarget} />
              {/each}
            </tbody>
          </table>
        </div>
        <div class="card containers-view__cards flex md:hidden">
          {#each stoppedNames as name (name)}
            {@const c = live.frame?.containers?.[name]}
            {#if c}
              <a class="containers-view__card" href={`#/containers/${encodeURIComponent(name)}`}>
                <div class="containers-view__card-head">
                  <HealthDot status={containerHealthStatus(c.state, c.health)} />
                  <ContainerIcon {name} icon={c.icon} size={20} />
                  <span class="containers-view__card-name">{name}</span>
                </div>
                <div class="containers-view__card-stats">
                  <span>{c.state}</span>
                </div>
              </a>
            {/if}
          {/each}
        </div>
      {/if}
    {/if}
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
  .containers-view__empty {
    margin: 0;
  }
  .containers-view__table-wrap {
    overflow-x: auto;
    padding: 0;
  }
  .containers-table {
    width: 100%;
    border-collapse: collapse;
    min-width: 72rem;
  }
  .containers-table thead th {
    padding: 0.5rem 0.6rem;
    text-align: left;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 12%, transparent);
    white-space: nowrap;
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
  .containers-view__cards {
    flex-direction: column;
    padding: 0;
  }
  /* Rail row, not a card -- was one .card per container (up to 20+ of
     them stacked); a hairline between rows instead, matching the disk
     list/event feed convention. */
  .containers-view__card {
    padding: 0.75rem 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
    text-decoration: none;
    color: var(--ink);
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 8%, transparent);
  }
  .containers-view__card:last-child {
    border-bottom: none;
  }
  .containers-view__card-head {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }
  .containers-view__card-name {
    font-weight: 500;
  }
  .containers-view__card-stats {
    display: flex;
    flex-wrap: wrap;
    gap: 0.75rem;
    font-size: 0.8rem;
    color: var(--ink-2);
    font-family: var(--font-mono);
  }
</style>
