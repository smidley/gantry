<script>
  import { live } from '../lib/sse.svelte';

  let mode = $derived(!live.frame ? 'connecting' : !live.connected ? 'disconnected' : live.stale ? 'stale' : null);
  let title = $derived(
    mode === 'connecting' ? 'Connecting to Gantry' : mode === 'disconnected' ? 'Live updates interrupted' : 'Updates are delayed',
  );
  let detail = $derived(
    mode === 'connecting'
      ? 'Preparing the first live system snapshot.'
      : mode === 'disconnected'
        ? 'Showing the most recently received data while Gantry reconnects automatically.'
        : 'The data stream is open, but a fresh update has not arrived on schedule.',
  );
</script>

{#if mode}
  <aside class="live-state" class:live-state--connecting={mode === 'connecting'} role={mode === 'connecting' ? 'status' : 'alert'}>
    <span class="live-state__icon" aria-hidden="true">
      {#if mode === 'connecting'}<i></i>{:else}!{/if}
    </span>
    <span class="live-state__copy">
      <strong>{title}</strong>
      <small>{detail}</small>
    </span>
    {#if mode !== 'connecting'}
      <button type="button" onclick={() => live.reconnect()}>Retry now</button>
    {/if}
  </aside>
{/if}

<style>
  .live-state {
    display: flex;
    align-items: center;
    gap: 0.7rem;
    min-height: 48px;
    padding: 0.55rem clamp(1rem, 2.5vw, 2.25rem);
    border-bottom: 1px solid color-mix(in oklab, var(--status-warning) 26%, var(--border));
    background: color-mix(in oklab, var(--status-warning) 8%, var(--surface));
    color: var(--ink);
  }
  .live-state--connecting {
    border-bottom-color: color-mix(in oklab, var(--accent) 20%, var(--border));
    background: color-mix(in oklab, var(--accent) 6%, var(--surface));
  }
  .live-state__icon {
    display: grid;
    width: 24px;
    height: 24px;
    flex-shrink: 0;
    place-items: center;
    border-radius: 50%;
    background: color-mix(in oklab, var(--status-warning) 15%, transparent);
    color: var(--status-warning);
    font-size: 0.76rem;
    font-weight: 750;
  }
  .live-state--connecting .live-state__icon {
    background: color-mix(in oklab, var(--accent) 12%, transparent);
  }
  .live-state__icon i {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--accent);
    animation: live-state-pulse 1.4s ease-in-out infinite;
  }
  @keyframes live-state-pulse {
    0%, 100% { box-shadow: 0 0 0 1px color-mix(in oklab, var(--accent) 30%, transparent); }
    50% { box-shadow: 0 0 0 6px color-mix(in oklab, var(--accent) 0%, transparent); }
  }
  .live-state__copy {
    display: flex;
    flex: 1;
    flex-direction: column;
    gap: 0.08rem;
    min-width: 0;
  }
  .live-state__copy strong { font-size: 0.76rem; font-weight: 650; }
  .live-state__copy small { color: var(--ink-2); font-size: 0.7rem; }
  .live-state button {
    min-height: 30px;
    padding: 0 0.65rem;
    border: 1px solid color-mix(in oklab, var(--status-warning) 30%, var(--border));
    border-radius: 7px;
    background: var(--surface);
    color: var(--ink);
    cursor: pointer;
    font-size: 0.7rem;
    font-weight: 600;
  }
  @media (max-width: 34rem) {
    .live-state { align-items: flex-start; padding: 0.7rem 1rem; }
    .live-state__copy small { line-height: 1.35; }
  }
  @media (prefers-reduced-motion: reduce) { .live-state__icon i { animation: none; } }
</style>
