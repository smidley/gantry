<script>
  import { onMount, untrack } from 'svelte';
  import { Tween } from 'svelte/motion';
  import { linear } from 'svelte/easing';
  import { fetchEvents } from '../lib/api';
  import { fmtBytes, fmtDuration, fmtPct, fmtRate, fmtRelTime } from '../lib/format';
  import { etaFromProgress, parityIsRunning } from '../lib/metrics';
  import HealthDot from './HealthDot.svelte';
  let { array, ts, glideMs, capacity, affectedDisks = [] } = $props();
  const EVENTS_POLL_MS = 30_000;
  let started = $derived(array['array.started']);
  let parityPct = $derived(array['parity.progress_pct']);
  let parityPctTween = new Tween(untrack(() => parityPct ?? 0), { duration: untrack(() => glideMs), easing: linear });
  $effect(() => {
    parityPctTween.set(parityPct ?? 0, { duration: glideMs, easing: linear });
  });
  let paritySpeed = $derived(array['parity.speed_bps']);
  let parityRunning = $derived(parityIsRunning(parityPct));
  let moverRunning = $derived(array['mover.running'] === 1);

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

  let parityHistory = $state([]);
  let historyError = $state(false);

  let parityHistorySeedPending = $state(true);

  async function loadParityHistory() {
    try {
      parityHistory = await fetchEvents({ kinds: ['parity.start', 'parity.finish'], limit: 5 });
      historyError = false;
    } catch {
      historyError = true;
      // A transient fetch failure leaves the last-good history showing
      // rather than blanking it -- the next poll or focus tries again.
    } finally {
      parityHistorySeedPending = false;
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

    <div class="storage-parity__summary">
    <div class="storage-parity__capacity">
      <span class="microlabel">Array capacity · {capacity.count} data disk{capacity.count === 1 ? '' : 's'}</span>
      <strong class="tabular-nums">{capacity.count ? `${fmtBytes(capacity.free)} free · ${fmtBytes(capacity.used)} used` : 'Array capacity is not available; see individual pools below'}</strong>
      {#if affectedDisks.length}<p class="microlabel">Check {affectedDisks.slice(0, 3).join(', ')}{affectedDisks.length > 3 ? ` and ${affectedDisks.length - 3} more` : ''}: disk errors or capacity above 90%.</p>{/if}
    </div>
    <div class="storage-parity__section">
      <span class="microlabel">Parity check</span>
      {#if parityRunning}
        <div class="storage-parity__progress">
          <div class="storage-parity__progress-track">
            <div
              class="storage-parity__progress-fill"
              style="width: {Math.min(100, Math.max(0, parityPctTween.current))}%; transition-duration: {glideMs}ms"
            ></div>
          </div>
          <span class="tabular-nums storage-parity__progress-pct">{fmtPct(parityPctTween.current)}</span>
          <span class="storage-parity__progress-detail tabular-nums">
            {fmtRate(paritySpeed ?? 0)} &middot; ETA {eta === null ? 'calculating…' : fmtDuration(eta)}
          </span>
        </div>
      {:else}
        <span class="storage-parity__idle">No check running</span>
      {/if}
    <div class="storage-parity__chips">
      <span class="storage-parity__chip" class:storage-parity__chip--active={moverRunning}>
        Mover {moverRunning ? 'running' : 'idle'}
      </span>
    </div>

    </div>

    <div class="storage-parity__section">
      <span class="microlabel">Recent checks</span>
      {#if parityHistorySeedPending}
        <!-- first loadParityHistory() call hasn't settled yet -- see parityHistorySeedPending's own doc -->
      {:else if historyError && parityHistory.length === 0}
        <p class="microlabel storage-parity__empty">Check history is temporarily unavailable.</p>
      {:else if parityHistory.length === 0}
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
  </div>

<style>
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
    /* duration is inline (transition-duration, above) -- see
       BaySchematic's matching fill for why a plain CSS transition
       (rather than a Tween/headState) is enough for one interpolated
       property. */
    transition-property: width;
    transition-timing-function: linear;
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


  .storage-parity__summary { display: grid; grid-template-columns: 1.2fr 1fr 1fr; align-items: start; gap: 1.5rem; }
  .storage-parity__summary > div { min-width: 0; }
  .storage-parity__capacity { display: grid; gap: .4rem; }
  .storage-parity__capacity p { margin: .25rem 0 0; }
  .storage-parity__progress { flex-wrap: wrap; }
  .storage-parity__progress-track { min-width: 5rem; }
  @media (max-width: 64rem) { .storage-parity__summary { grid-template-columns: 1fr; gap: 1rem; } }
</style>
