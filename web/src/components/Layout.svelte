<!--
  Layout: the app shell -- Sidebar (desktop) / TabBar (mobile), a
  header carrying the page title + LivePulse + ThemeToggle, and the
  route content area.
-->
<script>
  import Sidebar from './Sidebar.svelte';
  import TabBar from './TabBar.svelte';
  import LivePulse from './LivePulse.svelte';
  import ThemeToggle from './ThemeToggle.svelte';
  import CommandPalette from './CommandPalette.svelte';
  import LiveStateBanner from './LiveStateBanner.svelte';
  import { route } from '../lib/router';

  let { children } = $props();

  const ROUTE_CONTEXT = {
    overview: ['Monitor', 'System overview'],
    containers: ['Monitor', 'Containers'],
    'container-detail': ['Monitor', 'Container detail'],
    compare: ['Monitor', 'Compare'],
    top: ['Monitor', 'Metrics'],
    storage: ['Monitor', 'Storage'],
    maintenance: ['Operate', 'Maintenance'],
    gpu: ['Monitor', 'GPU'],
    insights: ['Monitor', 'Insights'],
    events: ['Operate', 'Events'],
    alerts: ['Operate', 'Alerts'],
    settings: ['System', 'Settings'],
    'not-found': ['Gantry', 'Not found'],
  };

  let context = $derived(ROUTE_CONTEXT[$route.name] ?? ROUTE_CONTEXT['not-found']);
</script>

<div class="layout">
  <Sidebar />
  <div class="layout__main-col">
    <header class="layout__header">
      <div class="layout__context" aria-label={`${context[0]}, ${context[1]}`}>
        <span class="layout__context-group">{context[0]}</span>
        <span class="layout__context-divider" aria-hidden="true">/</span>
        <span class="layout__context-page">{context[1]}</span>
      </div>
      <div class="layout__header-right">
        <CommandPalette />
        <LivePulse />
        <ThemeToggle />
      </div>
    </header>
    <LiveStateBanner />
    <main class="layout__content">
      {@render children?.()}
    </main>
  </div>
  <TabBar />
</div>

<style>
  .layout {
    display: flex;
    min-height: 100vh;
    position: relative;
  }
  .layout__main-col {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
  }
  .layout__header {
    position: sticky;
    top: 0;
    z-index: 8;
    display: flex;
    align-items: center;
    justify-content: space-between;
    min-height: 64px;
    padding: 0.7rem clamp(1rem, 2.5vw, 2.25rem);
    gap: 1rem;
    border-bottom: 1px solid color-mix(in oklab, var(--border) 78%, transparent);
    background: color-mix(in oklab, var(--page) 84%, transparent);
    backdrop-filter: blur(18px) saturate(130%);
  }
  .layout__context {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    min-width: 0;
    font-size: 0.78rem;
  }
  .layout__context-group {
    color: var(--ink-3);
    font-family: var(--font-mono);
    font-size: 0.69rem;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }
  .layout__context-divider {
    color: var(--border-strong);
  }
  .layout__context-page {
    color: var(--ink);
    font-weight: 620;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .layout__header-right {
    display: flex;
    align-items: center;
    gap: 0.75rem;
  }
  .layout__content {
    flex: 1;
    width: 100%;
    max-width: 1500px;
    margin: 0 auto;
    padding: clamp(1.25rem, 2.8vw, 2.5rem);
    /* Clears the fixed mobile TabBar (its own height + a little air). */
    padding-bottom: calc(1.5rem + 70px);
  }
  @media (min-width: 48rem) {
    .layout__content {
      padding-bottom: 1rem;
    }
  }

  @media (max-width: 47.9375rem) {
    .layout__header {
      min-height: 56px;
      padding: 0.55rem 1rem;
    }
    .layout__context-group,
    .layout__context-divider {
      display: none;
    }
    .layout__content {
      padding: 1.15rem 1rem calc(1.5rem + 70px);
    }
  }
</style>
