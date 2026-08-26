<!--
  Containers: the fleet table. Desktop (>=768px) gets a dense, sortable,
  filterable table; mobile gets a card list instead (same data, fewer
  columns). See the sorting $effect below for the SORT STABILITY
  contract: rows must not reorder just because a value ticked.
-->
<script>
  import { untrack } from 'svelte';
  import { live } from '../lib/sse.svelte';
  import { matchesContainerFilter, sortContainerNames } from '../lib/containersSort';
  import { containerHealthStatus } from '../lib/containerStatus';
  import { fmtBytes, fmtPct, fmtRate } from '../lib/format';
  import HealthDot from '../components/HealthDot.svelte';
  import ContainerRow from '../components/ContainerRow.svelte';

  const COLUMNS = [
    { key: 'health', label: '', sortable: true },
    { key: 'name', label: 'Name', sortable: true },
    { key: 'cpu', label: 'CPU', sortable: true },
    { key: 'mem', label: 'Mem', sortable: true },
    { key: 'net', label: 'Net', sortable: true },
    { key: 'io', label: 'IO', sortable: true },
    { key: 'gpu', label: 'GPU', sortable: true },
    { key: 'pids', label: 'PIDs', sortable: true },
    { key: 'uptime', label: 'Uptime', sortable: true },
    { key: 'image', label: 'Image', sortable: true },
  ];

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
              <th>
                <button type="button" class="containers-table__sort-btn" onclick={() => setSort(col.key)}>
                  <span class="microlabel">{col.label}{sortIndicator(col.key)}</span>
                </button>
              </th>
            {/each}
          </tr>
        </thead>
        <tbody>
          {#each runningNames as name (name)}
            <ContainerRow {name} />
          {/each}
        </tbody>
      </table>
    </div>

    <!-- Mobile: cards instead of table rows. -->
    <div class="containers-view__cards flex md:hidden">
      {#each runningNames as name (name)}
        {@const c = live.frame?.containers?.[name]}
        {#if c}
          <a class="card containers-view__card" href={`#/containers/${encodeURIComponent(name)}`}>
            <div class="containers-view__card-head">
              <HealthDot status={containerHealthStatus(c.state, c.health)} />
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
                <ContainerRow {name} />
              {/each}
            </tbody>
          </table>
        </div>
        <div class="containers-view__cards flex md:hidden">
          {#each stoppedNames as name (name)}
            {@const c = live.frame?.containers?.[name]}
            {#if c}
              <a class="card containers-view__card" href={`#/containers/${encodeURIComponent(name)}`}>
                <div class="containers-view__card-head">
                  <HealthDot status={containerHealthStatus(c.state, c.health)} />
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
    min-width: 62rem;
  }
  .containers-table thead th {
    padding: 0.5rem 0.6rem;
    text-align: left;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 12%, transparent);
    white-space: nowrap;
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
    gap: 0.5rem;
  }
  .containers-view__card {
    padding: 0.75rem 1rem;
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
