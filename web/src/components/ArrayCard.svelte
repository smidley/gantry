<!--
  ArrayCard: Overview's Unraid array summary -- state badge, parity
  progress (+speed +ETA while a check is running), a mover chip, the
  hottest disk's temperature, and per-pool usage bars. All from the live
  frame's unraid.array / disks maps; nothing here is fetched.
-->
<script>
  import { fmtBytes, fmtDuration, fmtPct, fmtRate } from '../lib/format';
  import { etaFromProgress, parityIsRunning, seqStep } from '../lib/metrics';
  import HealthDot from './HealthDot.svelte';

  let { array = {}, disks = {}, ts = 0 } = $props();

  let started = $derived(array['array.started']);
  let parityPct = $derived(array['parity.progress_pct']);
  let paritySpeed = $derived(array['parity.speed_bps']);
  // parityIsRunning treats an explicit 0 (the wire value var.go/fake.go
  // now both write on finish -- see its own doc) as idle, not merely
  // "key present" -- a bare `!== undefined` check would read that
  // finish-zero as still running forever, right back into the bug this
  // is fixing.
  let parityRunning = $derived(parityIsRunning(parityPct));
  let moverRunning = $derived(array['mover.running'] === 1);

  // eta is derived purely from parity.progress_pct's own rate of change
  // across live frames -- see etaFromProgress's doc for why speed_bps
  // (bytes/sec) can't be converted into a %/sec rate without a total-size
  // figure the DTO doesn't carry. prevSample is plain (non-reactive)
  // instance state: it only needs to survive between $effect runs, never
  // to trigger one itself.
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

  let hottestDisk = $derived.by(() => {
    let best = null;
    for (const [slot, m] of Object.entries(disks)) {
      const t = m['temp.c'];
      if (t !== undefined && (!best || t > best.temp)) best = { slot, temp: t };
    }
    return best;
  });

  // pools: every disk entity with a filesystem view (fs.used_bytes AND
  // fs.free_bytes present). Real disks.ini has no separate "this slot is
  // a pool vs. a plain array data disk" field this collector reads, so
  // "per-pool usage" here is every disk WITH a filesystem -- covering
  // both true cache/zfs pools and ordinary array data disks alike; parity
  // (no filesystem view at all) is correctly excluded by the same check.
  let pools = $derived.by(() => {
    const out = [];
    for (const [slot, m] of Object.entries(disks)) {
      const used = m['fs.used_bytes'];
      const free = m['fs.free_bytes'];
      if (used === undefined || free === undefined) continue;
      const total = used + free;
      out.push({ slot, used, total, pct: total > 0 ? (used / total) * 100 : 0 });
    }
    return out.sort((a, b) => a.slot.localeCompare(b.slot));
  });
</script>

<div class="card array-card">
  <div class="array-card__head">
    <span class="microlabel">Array</span>
    {#if started === 1}
      <HealthDot status="good" label="Started" />
    {:else if started === 0}
      <HealthDot status="serious" label="Stopped" />
    {:else}
      <span class="microlabel array-card__unknown">Unknown</span>
    {/if}
  </div>

  <div class="array-card__row">
    <span class="microlabel">Parity check</span>
    {#if parityRunning}
      <div class="array-card__parity">
        <div class="array-card__progress-track">
          <div class="array-card__progress-fill" style="width: {Math.min(100, Math.max(0, parityPct))}%"></div>
        </div>
        <span class="tabular-nums array-card__parity-pct">{fmtPct(parityPct)}</span>
        <span class="array-card__parity-detail tabular-nums">
          {fmtRate(paritySpeed ?? 0)} &middot; ETA {eta === null ? 'calculating…' : fmtDuration(eta)}
        </span>
      </div>
    {:else}
      <span class="array-card__idle">No check running</span>
    {/if}
  </div>

  <div class="array-card__row array-card__chips">
    <span class="array-card__chip" class:array-card__chip--active={moverRunning}>
      Mover {moverRunning ? 'running' : 'idle'}
    </span>
    {#if hottestDisk}
      <span class="array-card__chip">Hottest: {hottestDisk.slot} {hottestDisk.temp.toFixed(1)}&deg;C</span>
    {/if}
  </div>

  {#if pools.length > 0}
    <div class="array-card__pools">
      {#each pools as pool (pool.slot)}
        <div class="array-card__pool-row">
          <span class="array-card__pool-name">{pool.slot}</span>
          <div class="array-card__pool-track">
            <div class="array-card__pool-fill" style="width: {pool.pct}%; background: {seqStep(pool.pct)}"></div>
          </div>
          <span class="tabular-nums array-card__pool-pct">{fmtPct(pool.pct)}</span>
          <span class="tabular-nums array-card__pool-bytes">{fmtBytes(pool.used)} / {fmtBytes(pool.total)}</span>
        </div>
      {/each}
    </div>
  {:else}
    <p class="microlabel array-card__empty">No disk usage data yet.</p>
  {/if}
</div>

<style>
  .array-card {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }
  .array-card__head {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  .array-card__unknown {
    color: var(--ink-2);
  }
  .array-card__row {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
  }
  .array-card__parity {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    flex-wrap: wrap;
  }
  .array-card__progress-track {
    flex: 1;
    min-width: 6rem;
    height: 10px;
    border-radius: 5px;
    background: color-mix(in oklab, var(--ink) 8%, transparent);
    overflow: hidden;
  }
  .array-card__progress-fill {
    height: 100%;
    background: var(--series-1);
  }
  .array-card__parity-pct {
    font-family: var(--font-mono);
    font-size: 0.85rem;
    min-width: 3.2em;
  }
  .array-card__parity-detail {
    font-family: var(--font-mono);
    font-size: 0.75rem;
    color: var(--ink-2);
  }
  .array-card__idle {
    color: var(--ink-2);
    font-size: 0.85rem;
  }
  .array-card__chips {
    flex-direction: row;
    flex-wrap: wrap;
    gap: 0.5rem;
  }
  .array-card__chip {
    font-family: var(--font-mono);
    font-size: 0.72rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    padding: 0.3rem 0.55rem;
    border-radius: 999px;
    background: color-mix(in oklab, var(--ink) 7%, transparent);
    color: var(--ink-2);
  }
  .array-card__chip--active {
    background: color-mix(in oklab, var(--status-good) 18%, transparent);
    color: var(--status-good);
  }
  .array-card__pools {
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
  }
  .array-card__pool-row {
    display: grid;
    grid-template-columns: 4.5rem 1fr auto auto;
    align-items: center;
    gap: 0.5rem;
  }
  .array-card__pool-name {
    font-family: var(--font-mono);
    font-size: 0.78rem;
    color: var(--ink-2);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .array-card__pool-track {
    height: 8px;
    border-radius: 4px;
    background: color-mix(in oklab, var(--ink) 8%, transparent);
    overflow: hidden;
  }
  .array-card__pool-fill {
    height: 100%;
  }
  .array-card__pool-pct {
    font-size: 0.75rem;
    min-width: 3em;
    text-align: right;
  }
  .array-card__pool-bytes {
    font-size: 0.72rem;
    color: var(--ink-2);
    white-space: nowrap;
  }
  .array-card__empty {
    margin: 0;
  }
</style>
