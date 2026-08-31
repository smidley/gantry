<!--
  TabBar: mobile (<768px) bottom nav, rendered from the same routes
  table as Sidebar. min-width 46px per item keeps all 8 within a 375px
  viewport (8*46=368px, Maintenance's addition is what pushed 7*50's own
  exact fit past it); overflow-x:auto is a safety net, not the expected
  path. Labels wrap at word boundaries within their own ~46px column
  (see the .microlabel override below for why that needs an explicit
  width) -- mobileLabel supplies a soft-hyphen break point for the
  labels wide enough to need one even after that.
-->
<script>
  import { route, routes } from '../lib/router';

  const primaryNames = new Set(['overview', 'containers', 'top', 'storage', 'alerts']);
  const primaryRoutes = routes.filter((item) => primaryNames.has(item.name));
  const moreRoutes = routes.filter((item) => !primaryNames.has(item.name));
  const MORE_ICON = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><circle cx="5" cy="12" r="1" fill="currentColor"/><circle cx="12" cy="12" r="1" fill="currentColor"/><circle cx="19" cy="12" r="1" fill="currentColor"/></svg>';

  let moreOpen = $state(false);
  let moreActive = $derived(moreRoutes.some((item) => item.name === $route.name));
</script>

<nav class="tab-bar flex md:hidden" aria-label="Primary">
  {#if moreOpen}
    <div class="tab-bar__more-menu" role="menu" aria-label="More destinations">
      <div class="tab-bar__more-head">
        <span>More</span>
        <button type="button" onclick={() => (moreOpen = false)} aria-label="Close more menu">&times;</button>
      </div>
      <div class="tab-bar__more-grid">
        {#each moreRoutes as item (item.name)}
          <a
            href={item.hash}
            role="menuitem"
            class="tab-bar__more-item"
            class:tab-bar__more-item--active={$route.name === item.name}
            onclick={() => (moreOpen = false)}
          >
            <span class="tab-bar__more-icon">{@html item.icon}</span>
            <span>{item.label}</span>
          </a>
        {/each}
      </div>
    </div>
  {/if}

  {#each primaryRoutes as item (item.name)}
    <a href={item.hash} class="tab-bar__item" class:tab-bar__item--active={$route.name === item.name}>
      <span class="tab-bar__icon">{@html item.icon}</span>
      <span class="microlabel">{item.mobileLabel ?? item.label}</span>
    </a>
  {/each}
  <button
    type="button"
    class="tab-bar__item tab-bar__more-trigger"
    class:tab-bar__item--active={moreActive || moreOpen}
    aria-expanded={moreOpen}
    aria-label="More destinations"
    onclick={() => (moreOpen = !moreOpen)}
  >
    <span class="tab-bar__icon">{@html MORE_ICON}</span>
    <span class="microlabel">More</span>
  </button>
</nav>

<style>
  .tab-bar {
    position: fixed;
    left: 0.65rem;
    right: 0.65rem;
    bottom: calc(0.55rem + env(safe-area-inset-bottom));
    z-index: 20;
    padding: 0.25rem;
    background: color-mix(in oklab, var(--sidebar) 94%, transparent);
    border: 1px solid rgb(255 255 255 / 0.08);
    border-radius: 15px;
    box-shadow: 0 14px 42px rgb(0 0 0 / 0.28);
    backdrop-filter: blur(18px) saturate(130%);
    overflow: visible;
  }
  .tab-bar__item {
    flex: 1 1 0;
    min-width: 0;
    min-height: 52px;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 0.15rem;
    color: var(--sidebar-muted);
    text-decoration: none;
    padding: 0.3rem 0.2rem;
    border-radius: 11px;
    transition: color 150ms ease, background 150ms ease;
  }
  .tab-bar__item--active {
    color: var(--sidebar-ink);
    background: var(--sidebar-surface);
  }
  .tab-bar__more-trigger {
    border: 0;
    font: inherit;
    cursor: pointer;
  }
  .tab-bar__icon {
    display: inline-flex;
    width: 20px;
    height: 20px;
  }
  .tab-bar__icon :global(svg) {
    width: 100%;
    height: 100%;
  }
  /* align-items: center on the item above shrink-wraps this span to
     its unwrapped content width (e.g. "Top Consumers" on one line)
     rather than constraining it to the item's own ~53px share of a
     375px viewport, which spills text into neighboring items. Forcing
     the full width here makes it wrap within its own column instead. */
  .tab-bar__item .microlabel {
    width: 100%;
    text-align: center;
    line-height: 1.15;
    font-size: 9px;
    letter-spacing: 0.02em;
    overflow-wrap: break-word;
    color: inherit;
    font-family: var(--font-sans);
    font-weight: 600;
    text-transform: none;
  }
  .tab-bar__more-menu {
    position: absolute;
    left: 0;
    right: 0;
    bottom: calc(100% + 0.55rem);
    padding: 0.75rem;
    color: var(--sidebar-ink);
    background: var(--sidebar);
    border: 1px solid rgb(255 255 255 / 0.08);
    border-radius: 15px;
    box-shadow: 0 16px 46px rgb(0 0 0 / 0.32);
    backdrop-filter: blur(18px) saturate(130%);
  }
  .tab-bar__more-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0 0.2rem 0.6rem;
    color: var(--sidebar-muted);
    font-family: var(--font-mono);
    font-size: 0.63rem;
    text-transform: uppercase;
    letter-spacing: 0.1em;
  }
  .tab-bar__more-head button {
    width: 32px;
    height: 32px;
    border: 0;
    color: var(--sidebar-muted);
    background: transparent;
    font-size: 1.1rem;
    cursor: pointer;
  }
  .tab-bar__more-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 0.35rem;
  }
  .tab-bar__more-item {
    min-height: 48px;
    display: flex;
    align-items: center;
    gap: 0.65rem;
    padding: 0.6rem 0.7rem;
    border-radius: 10px;
    color: var(--sidebar-muted);
    text-decoration: none;
    font-size: 0.78rem;
    font-weight: 600;
  }
  .tab-bar__more-item--active,
  .tab-bar__more-item:hover {
    color: var(--sidebar-ink);
    background: var(--sidebar-surface);
  }
  .tab-bar__more-icon {
    width: 18px;
    height: 18px;
    display: inline-flex;
  }
  .tab-bar__more-icon :global(svg) {
    width: 100%;
    height: 100%;
  }
</style>
