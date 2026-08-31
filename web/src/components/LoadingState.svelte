<script>
  let { title = 'Loading live data', detail = 'Gantry is preparing this view.' } = $props();
</script>

<section class="loading-state" aria-live="polite" aria-busy="true">
  <div class="loading-state__head">
    <span class="loading-state__pulse" aria-hidden="true"></span>
    <div><h1>{title}</h1><p>{detail}</p></div>
  </div>
  <div class="loading-state__grid" aria-hidden="true"><span></span><span></span><span></span></div>
</section>

<style>
  .loading-state { display: flex; flex-direction: column; gap: 1rem; }
  .loading-state__head {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 1.2rem;
    border: 1px solid var(--border);
    border-radius: 14px;
    background: var(--surface-raised);
  }
  .loading-state__pulse {
    width: 11px;
    height: 11px;
    flex-shrink: 0;
    border-radius: 50%;
    background: var(--accent);
    box-shadow: 0 0 0 5px color-mix(in oklab, var(--accent) 10%, transparent);
    animation: loading-state-pulse 1.4s ease-in-out infinite;
  }
  .loading-state h1 { margin: 0; color: var(--ink); font-size: 1rem; }
  .loading-state p { margin: 0.2rem 0 0; color: var(--ink-2); font-size: 0.78rem; }
  .loading-state__grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 0.75rem; }
  .loading-state__grid span {
    height: 8rem;
    border: 1px solid var(--border);
    border-radius: 12px;
    background: linear-gradient(100deg, var(--surface-soft) 20%, color-mix(in oklab, var(--accent) 7%, var(--surface)) 50%, var(--surface-soft) 80%);
    background-size: 220% 100%;
    animation: loading-state-shimmer 1.8s ease-in-out infinite;
  }
  @keyframes loading-state-pulse { 50% { opacity: 0.45; transform: scale(0.86); } }
  @keyframes loading-state-shimmer { to { background-position: -220% 0; } }
  @media (max-width: 40rem) {
    .loading-state__grid { grid-template-columns: 1fr; }
    .loading-state__grid span { height: 5.5rem; }
  }
  @media (prefers-reduced-motion: reduce) {
    .loading-state__pulse, .loading-state__grid span { animation: none; }
  }
</style>
