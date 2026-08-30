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
</script>

<nav class="tab-bar flex md:hidden" aria-label="Primary">
  {#each routes as item (item.name)}
    <a href={item.hash} class="tab-bar__item" class:tab-bar__item--active={$route.name === item.name}>
      <span class="tab-bar__icon">{@html item.icon}</span>
      <span class="microlabel">{item.mobileLabel ?? item.label}</span>
    </a>
  {/each}
</nav>

<style>
  .tab-bar {
    position: fixed;
    left: 0;
    right: 0;
    bottom: 0;
    z-index: 10;
    background: var(--surface);
    border-top: 1px solid color-mix(in oklab, var(--ink) 8%, transparent);
    overflow-x: auto;
  }
  .tab-bar__item {
    flex: 1 1 0;
    min-width: 46px;
    min-height: 48px;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 0.15rem;
    color: var(--ink-2);
    text-decoration: none;
    padding: 0.25rem;
  }
  .tab-bar__item--active {
    color: var(--series-1);
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
  }
</style>
