<!--
  Storage: the array/disk detail view. Disk grid (one card per disk
  entity, grouped parity -> data -> cache/pools -> flash), a parity
  card (state + progress/speed/ETA + a short start/finish history), a
  mover chip, a shares table, and a docker storage card -- all read
  straight off the live SSE frame except the parity history, which
  isn't in the frame at all (events never are -- see sse.svelte.ts's
  own doc) and is polled the same way Overview polls its events feed.
-->
<script>
  import { onMount } from 'svelte';
  import { live } from '../lib/sse.svelte';
  import { fetchEvents } from '../lib/api';
  import { fmtBytes, fmtDuration, fmtPct, fmtRate, fmtRelTime } from '../lib/format';
  import { etaFromProgress, seqStep, sharesFromMetrics } from '../lib/metrics';
  import { diskRole, diskTempState, diskUsagePct, sortDiskEntities } from '../lib/disks';
  import HealthDot from '../components/HealthDot.svelte';

  const EVENTS_POLL_MS = 30_000;
  const ROLE_LABEL = { parity: 'Parity', data: 'Data disk', pool: 'Cache / pool', flash: 'Boot (flash)' };

  let disks = $derived(live.frame?.disks ?? {});
  let diskNames = $derived(sortDiskEntities(Object.keys(disks)));
  let array = $derived(live.frame?.unraid?.array ?? {});
  let dockerStorage = $derived(live.frame?.unraid?.docker ?? {});
  let sources = $derived(live.frame?.sources ?? {});
  let ts = $derived(live.frame?.ts ?? 0);

  let started = $derived(array['array.started']);
  let parityPct = $derived(array['parity.progress_pct']);
  let paritySpeed = $derived(array['parity.speed_bps']);
  let parityRunning = $derived(parityPct !== undefined);
  let moverRunning = $derived(array['mover.running'] === 1);
  let shares = $derived(sharesFromMetrics(array));

  // eta: identical shape to ArrayCard's own effect (see its doc for why
  // this is derived from parity.progress_pct's own rate of change,
  // never from speed_bps). prevSample is plain instance state -- it only
  // needs to survive between effect runs, never to trigger one itself.
  let prevSample = null;
  let eta = $state(null);
  $effect(() => {
    if (!parityRunning || parityPct === undefined) {
      prevSample = null;
      eta = null;
      return;
    }
    if (prevSample) {
      eta = etaFromProgress(prevSample.ts, prevSample.pct, ts, parityPct);
    }
    prevSample = { ts, pct: parityPct };
  });

  // parityHistory: not in the live frame (events never are) -- fetched
  // once on mount, then re-polled every 30s and on window focus, the
  // same low-urgency background-refresh pattern Overview's own events
  // feed uses (a parity run lasts many minutes to hours, so 30s latency
  // on "did it just start/finish" is harmless; no AbortController is
  // needed here for the same reason it isn't in Overview's loadEvents --
  // this isn't a rapid, user-selector-driven fetch that can race itself).
  let parityHistory = $state([]);
  async function loadParityHistory() {
    try {
      parityHistory = await fetchEvents({ kinds: ['parity.start', 'parity.finish'], limit: 5 });
    } catch {
      // A transient fetch failure leaves the last-good history showing
      // rather than blanking it -- the next poll or focus tries again.
    }
  }
  onMount(() => {
    loadParityHistory();
    const interval = setInterval(loadParityHistory, EVENTS_POLL_MS);
    window.addEventListener('focus', loadParityHistory);
    return () => {
      clearInterval(interval);
      window.removeEventListener('focus', loadParityHistory);
    };
  });

  function historyLabel(event) {
    if (event.Kind === 'parity.start') return 'Started';
    return event.Detail ? `Finished · ${event.Detail}` : 'Finished';
  }
</script>

<div class="storage-view">
  <h1 class="page-title">Storage</h1>

  <div class="card storage-parity">
    <div class="storage-parity__head">
      <span class="microlabel">Array</span>
      {#if started === 1}
        <HealthDot status="good" label="Started" />
      {:else if started === 0}
        <HealthDot status="serious" label="Stopped" />
      {:else}
        <span class="microlabel storage-parity__unknown">Unknown</span>
      {/if}
    </div>

    <div class="storage-parity__section">
      <span class="microlabel">Parity check</span>
      {#if parityRunning}
        <div class="storage-parity__progress">
          <div class="storage-parity__progress-track">
            <div class="storage-parity__progress-fill" style="width: {Math.min(100, Math.max(0, parityPct))}%"></div>
          </div>
          <span class="tabular-nums storage-parity__progress-pct">{fmtPct(parityPct)}</span>
          <span class="storage-parity__progress-detail tabular-nums">
            {fmtRate(paritySpeed ?? 0)} &middot; ETA {eta === null ? 'calculating…' : fmtDuration(eta)}
          </span>
        </div>
      {:else}
        <span class="storage-parity__idle">No check running</span>
      {/if}
    </div>

    <div class="storage-parity__chips">
      <span class="storage-parity__chip" class:storage-parity__chip--active={moverRunning}>
        Mover {moverRunning ? 'running' : 'idle'}
      </span>
    </div>

    <div class="storage-parity__section">
      <span class="microlabel">Recent checks</span>
      {#if parityHistory.length === 0}
        <p class="microlabel storage-parity__empty">No parity check history yet.</p>
      {:else}
        <ul class="storage-parity__history">
          {#each parityHistory as event (event.ID)}
            <li>
              <span>{historyLabel(event)}</span>
              <span class="microlabel storage-parity__history-time">{fmtRelTime(event.TS)}</span>
            </li>
          {/each}
        </ul>
      {/if}
    </div>
  </div>

  {#if diskNames.length === 0}
    <p class="microlabel storage-view__empty">
      No disk data yet.{sources.unraid && sources.unraid !== 'ok' ? ` ${sources.unraid}` : ''}
    </p>
  {:else}
    <div class="storage-view__disk-grid">
      {#each diskNames as name (name)}
        {@const metrics = disks[name]}
        {@const role = diskRole(name)}
        {@const temp = diskTempState(metrics)}
        {@const usagePct = diskUsagePct(metrics)}
        {@const errors = metrics['errors'] ?? 0}
        <div class="card storage-disk">
          <div class="storage-disk__head">
            <span class="microlabel">{ROLE_LABEL[role]}</span>
            {#if temp.kind === 'reading'}
              <span class="tabular-nums storage-disk__temp">{temp.celsius.toFixed(1)}&deg;C</span>
            {:else}
              <span class="storage-disk__chip">{temp.kind === 'spun-down' ? 'Spun down' : 'No sensor'}</span>
            {/if}
          </div>
          <div class="storage-disk__name">{name}</div>

          {#if usagePct !== null}
            <div class="storage-disk__usage">
              <div class="storage-disk__usage-track">
                <div
                  class="storage-disk__usage-fill"
                  style="width: {usagePct}%; background: {seqStep(usagePct)}"
                ></div>
              </div>
              <span class="tabular-nums storage-disk__usage-pct">{fmtPct(usagePct)}</span>
            </div>
            <div class="tabular-nums storage-disk__bytes">
              {fmtBytes(metrics['fs.used_bytes'])} / {fmtBytes(metrics['fs.used_bytes'] + metrics['fs.free_bytes'])}
            </div>
            {#if usagePct > 90}
              <HealthDot status="warning" label="High usage" />
            {/if}
          {/if}

          {#if errors > 0}
            <HealthDot status="serious" label={`${errors} error${errors === 1 ? '' : 's'}`} />
          {/if}
        </div>
      {/each}
    </div>
  {/if}

  <div class="storage-view__row">
    <div class="card storage-shares">
      <span class="microlabel">Shares</span>
      <p class="microlabel storage-shares__caption">
        Share sizes are the backing array or pool total — Unraid doesn't track true per-share usage.
      </p>
      {#if shares.length === 0}
        <p class="microlabel storage-shares__empty">
          No share data yet.{sources.unraid && sources.unraid !== 'ok' ? ` ${sources.unraid}` : ''}
        </p>
      {:else}
        <div class="storage-shares__table-wrap">
          <table class="storage-shares__table">
            <thead>
              <tr>
                <th class="microlabel">Share</th>
                <th class="microlabel">Used</th>
              </tr>
            </thead>
            <tbody>
              {#each shares as share (share.name)}
                <tr>
                  <td>{share.name}</td>
                  <td class="tabular-nums">{fmtBytes(share.usedBytes)}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </div>

    <div class="card storage-docker">
      <span class="microlabel">Docker storage</span>
      {#if dockerStorage['docker.images_bytes'] === undefined && dockerStorage['docker.containers_bytes'] === undefined && dockerStorage['docker.volumes_bytes'] === undefined}
        <p class="microlabel storage-docker__empty">
          No docker storage data yet.{sources['docker-disk'] && sources['docker-disk'] !== 'ok'
            ? ` ${sources['docker-disk']}`
            : ''}
        </p>
      {:else}
        <dl class="storage-docker__list">
          <dt>Images</dt>
          <dd class="tabular-nums">{fmtBytes(dockerStorage['docker.images_bytes'] ?? 0)}</dd>
          <dt>Containers</dt>
          <dd class="tabular-nums">{fmtBytes(dockerStorage['docker.containers_bytes'] ?? 0)}</dd>
          <dt>Volumes</dt>
          <dd class="tabular-nums">{fmtBytes(dockerStorage['docker.volumes_bytes'] ?? 0)}</dd>
        </dl>
      {/if}
    </div>
  </div>
</div>

<style>
  .storage-view {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }
  .storage-view__empty {
    margin: 0;
  }
  .storage-parity {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }
  .storage-parity__head {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  .storage-parity__unknown {
    color: var(--ink-2);
  }
  .storage-parity__section {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
  }
  .storage-parity__progress {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    flex-wrap: wrap;
  }
  .storage-parity__progress-track {
    flex: 1;
    min-width: 6rem;
    height: 10px;
    border-radius: 5px;
    background: color-mix(in oklab, var(--ink) 8%, transparent);
    overflow: hidden;
  }
  .storage-parity__progress-fill {
    height: 100%;
    background: var(--series-1);
  }
  .storage-parity__progress-pct {
    font-family: var(--font-mono);
    font-size: 0.85rem;
    min-width: 3.2em;
  }
  .storage-parity__progress-detail {
    font-family: var(--font-mono);
    font-size: 0.75rem;
    color: var(--ink-2);
  }
  .storage-parity__idle {
    color: var(--ink-2);
    font-size: 0.85rem;
  }
  .storage-parity__chips {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
  }
  .storage-parity__chip {
    font-family: var(--font-mono);
    font-size: 0.72rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    padding: 0.3rem 0.55rem;
    border-radius: 999px;
    background: color-mix(in oklab, var(--ink) 7%, transparent);
    color: var(--ink-2);
  }
  .storage-parity__chip--active {
    background: color-mix(in oklab, var(--status-good) 18%, transparent);
    color: var(--status-good);
  }
  .storage-parity__empty {
    margin: 0;
  }
  .storage-parity__history {
    margin: 0;
    padding: 0;
    list-style: none;
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
  }
  .storage-parity__history li {
    display: flex;
    justify-content: space-between;
    gap: 0.75rem;
    font-size: 0.85rem;
  }
  .storage-parity__history-time {
    white-space: nowrap;
  }

  .storage-view__disk-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(12rem, 1fr));
    gap: 0.75rem;
  }
  .storage-disk {
    padding: 0.85rem 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .storage-disk__head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
  }
  .storage-disk__temp {
    font-size: 0.85rem;
  }
  .storage-disk__chip {
    font-family: var(--font-mono);
    font-size: 0.7rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    padding: 0.2rem 0.5rem;
    border-radius: 999px;
    background: color-mix(in oklab, var(--ink) 7%, transparent);
    color: var(--ink-2);
    white-space: nowrap;
  }
  .storage-disk__name {
    font-family: var(--font-display);
    font-weight: 600;
    font-size: 1rem;
    color: var(--ink);
  }
  .storage-disk__usage {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }
  .storage-disk__usage-track {
    flex: 1;
    height: 8px;
    border-radius: 4px;
    background: color-mix(in oklab, var(--ink) 8%, transparent);
    overflow: hidden;
  }
  .storage-disk__usage-fill {
    height: 100%;
  }
  .storage-disk__usage-pct {
    font-size: 0.78rem;
    min-width: 3em;
    text-align: right;
  }
  .storage-disk__bytes {
    font-size: 0.75rem;
    color: var(--ink-2);
  }

  .storage-view__row {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: 0.75rem;
    align-items: start;
  }
  @media (max-width: 47.9375rem) {
    .storage-view__row {
      grid-template-columns: 1fr;
    }
  }
  .storage-shares,
  .storage-docker {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .storage-shares__caption {
    margin: 0;
    text-transform: none;
    letter-spacing: normal;
  }
  .storage-shares__empty,
  .storage-docker__empty {
    margin: 0;
  }
  .storage-shares__table-wrap {
    overflow-x: auto;
  }
  .storage-shares__table {
    width: 100%;
    border-collapse: collapse;
    min-width: 16rem;
  }
  .storage-shares__table th {
    text-align: left;
    padding: 0.35rem 0.5rem;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 12%, transparent);
  }
  .storage-shares__table td {
    padding: 0.35rem 0.5rem;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 6%, transparent);
    font-size: 0.85rem;
  }
  .storage-shares__table th:last-child,
  .storage-shares__table td:last-child {
    text-align: right;
  }
  .storage-docker__list {
    display: grid;
    grid-template-columns: auto 1fr;
    gap: 0.35rem 1rem;
    margin: 0;
  }
  .storage-docker__list dt {
    color: var(--ink-2);
    font-family: var(--font-mono);
    font-size: 0.78rem;
  }
  .storage-docker__list dd {
    margin: 0;
    font-size: 0.85rem;
    text-align: right;
  }
</style>
