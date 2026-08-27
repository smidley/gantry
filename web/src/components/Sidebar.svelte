<!--
  Sidebar: desktop (>=768px) primary nav, rendered from the single
  routes table in lib/router.ts (shared with TabBar so the two
  presentations can never drift).
-->
<script>
  import { route, routes } from '../lib/router';
</script>

<nav class="sidebar hidden md:flex" aria-label="Primary">
  {#each routes as item (item.name)}
    <a href={item.hash} class="sidebar__item" class:sidebar__item--active={$route.name === item.name}>
      <span class="sidebar__icon">{@html item.icon}</span>
      <span class="sidebar__label">{item.label}</span>
    </a>
  {/each}
</nav>

<style>
  .sidebar {
    flex-direction: column;
    width: 200px;
    flex-shrink: 0;
    padding: 0.75rem;
    gap: 0.25rem;
    border-right: 1px solid color-mix(in oklab, var(--ink) 8%, transparent);
  }
  .sidebar__item {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    padding: 0.55rem 0.65rem;
    min-height: 40px;
    border-radius: 6px;
    color: var(--ink-2);
    text-decoration: none;
    font-size: 0.9rem;
  }
  .sidebar__item:hover {
    background: color-mix(in oklab, var(--ink) 6%, transparent);
  }
  .sidebar__item--active {
    background: color-mix(in oklab, var(--series-1) 12%, transparent);
    color: var(--ink);
    font-weight: 500;
  }
  .sidebar__icon {
    display: inline-flex;
    width: 18px;
    height: 18px;
    flex-shrink: 0;
  }
  .sidebar__icon :global(svg) {
    width: 100%;
    height: 100%;
  }
</style>
