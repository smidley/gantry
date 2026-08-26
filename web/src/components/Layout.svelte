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

  let { children } = $props();
</script>

<div class="layout">
  <Sidebar />
  <div class="layout__main-col">
    <header class="layout__header">
      <span class="layout__title">Gantry</span>
      <div class="layout__header-right">
        <LivePulse />
        <ThemeToggle />
      </div>
    </header>
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
  }
  .layout__main-col {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
  }
  .layout__header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.75rem 1rem;
    gap: 1rem;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 8%, transparent);
  }
  .layout__title {
    font-family: var(--font-display);
    font-weight: 700;
    font-size: 1.1rem;
    letter-spacing: 0.01em;
  }
  .layout__header-right {
    display: flex;
    align-items: center;
    gap: 0.75rem;
  }
  .layout__content {
    flex: 1;
    padding: 1rem;
    /* Clears the fixed mobile TabBar (its own height + a little air). */
    padding-bottom: calc(1rem + 56px);
  }
  @media (min-width: 48rem) {
    .layout__content {
      padding-bottom: 1rem;
    }
  }
</style>
