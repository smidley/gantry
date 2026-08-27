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
  <span class="microlabel live-pulse__text">{statusText}</span>
</span>

<style>
  .live-pulse {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
  }
  .live-pulse__dot {
    position: relative;
    display: inline-block;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--status-good);
  }
  .live-pulse__ring {
    position: absolute;
    inset: -6px;
    border-radius: 50%;
    background: var(--status-good);
    opacity: 0.5;
    animation: live-pulse-ring 900ms ease-out;
  }
  @keyframes live-pulse-ring {
    from {
      opacity: 0.5;
      transform: scale(0.4);
    }
    to {
      opacity: 0;
      transform: scale(1.8);
    }
  }
  .live-pulse--stale .live-pulse__dot,
  .live-pulse--stale .live-pulse__ring {
    background: var(--status-warning);
  }
  .live-pulse--off .live-pulse__dot {
    background: transparent;
    border: 1.5px solid var(--ink-2);
  }
  .live-pulse__text {
    min-width: 6.5em;
  }
</style>
