<!--
  LivePulse: the one signature/bold element in the whole UI (everything
  else stays quiet). A small dot + "live · 2s" micro-label; the dot
  emits one soft radial pulse PER RECEIVED SSE FRAME -- retriggered by
  recreating the ring element inside a {#key live.frameCount} block,
  not a free-running CSS loop, so it's honest about whether frames are
  actually arriving. >6s without a frame -> amber dot, text "stale".
  Disconnected -> hollow dot, text "reconnecting…". Lives in Layout's
  header.
-->
<script>
  import { live } from '../lib/sse.svelte';

  let statusText = $derived(!live.connected ? 'reconnecting…' : live.stale ? 'stale' : 'live · 2s');
</script>

<span
  class="live-pulse"
  class:live-pulse--stale={live.connected && live.stale}
  class:live-pulse--off={!live.connected}
>
  {#key live.frameCount}
    <span class="live-pulse__dot" aria-hidden="true">
      {#if live.frameCount > 0}
        <span class="live-pulse__ring"></span>
      {/if}
    </span>
  {/key}
  <span class="live-pulse__text">{statusText}</span>
</span>

<style>
  .live-pulse {
    display: inline-flex;
    align-items: center;
    gap: 0.52rem;
    min-height: 34px;
    padding: 0.35rem 0.72rem;
    border: 1px solid var(--border);
    border-radius: 999px;
    background: color-mix(in oklab, var(--surface) 84%, transparent);
    box-shadow: 0 1px 2px color-mix(in oklab, var(--ink) 7%, transparent);
  }
  .live-pulse__dot {
    position: relative;
    display: inline-block;
    width: 7px;
    height: 7px;
    flex-shrink: 0;
    border-radius: 50%;
    background: var(--status-good);
    box-shadow: 0 0 0 3px color-mix(in oklab, var(--status-good) 13%, transparent);
  }
  .live-pulse__ring {
    position: absolute;
    inset: -4px;
    border-radius: 50%;
    border: 1px solid var(--status-good);
    opacity: 0;
    animation: live-pulse-ring 1400ms cubic-bezier(0.16, 1, 0.3, 1);
  }
  @keyframes live-pulse-ring {
    from {
      opacity: 0.42;
      transform: scale(0.72);
    }
    to {
      opacity: 0;
      transform: scale(1.7);
    }
  }
  .live-pulse--stale .live-pulse__dot {
    background: var(--status-warning);
    box-shadow: 0 0 0 3px color-mix(in oklab, var(--status-warning) 13%, transparent);
  }
  .live-pulse--stale .live-pulse__ring {
    border-color: var(--status-warning);
  }
  .live-pulse--off .live-pulse__dot {
    background: transparent;
    border: 1.5px solid var(--ink-3);
    box-shadow: none;
  }
  .live-pulse--off .live-pulse__ring {
    display: none;
  }
  .live-pulse__text {
    min-width: 5.65em;
    color: var(--ink-2);
    font-size: 0.72rem;
    font-weight: 620;
    line-height: 1;
    letter-spacing: 0.035em;
    text-transform: none;
    white-space: nowrap;
  }

  @media (max-width: 34rem) {
    .live-pulse {
      min-height: 32px;
      padding: 0.3rem 0.58rem;
    }
    .live-pulse__text {
      min-width: auto;
      font-size: 0.68rem;
    }
  }
</style>
