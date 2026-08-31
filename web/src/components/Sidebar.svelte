<!--
  Sidebar: desktop (>=768px) primary nav, rendered from the single
  routes table in lib/router.ts (shared with TabBar so the two
  presentations can never drift).
-->
<script>
  import { route, routes } from '../lib/router';

  const monitorRoutes = routes.filter((item) =>
    ['overview', 'containers', 'top', 'storage', 'gpu', 'insights'].includes(item.name),
  );
  const operateRoutes = routes.filter((item) => ['maintenance', 'events', 'alerts'].includes(item.name));
  const systemRoutes = routes.filter((item) => item.name === 'settings');
</script>

<nav class="sidebar hidden md:flex" aria-label="Primary">
  <a class="sidebar__brand" href="#/" aria-label="Gantry overview">
    <span class="sidebar__brand-mark" aria-hidden="true">
      <span></span><span></span><span></span>
    </span>
    <span class="sidebar__brand-copy">
      <strong>Gantry</strong>
      <small>Server observability</small>
    </span>
  </a>

  <div class="sidebar__nav">
    <div class="sidebar__group">
      <span class="sidebar__group-label">Monitor</span>
      {#each monitorRoutes as item (item.name)}
        <a href={item.hash} class="sidebar__item" class:sidebar__item--active={$route.name === item.name}>
          <span class="sidebar__icon">{@html item.icon}</span>
          <span class="sidebar__label">{item.label}</span>
          {#if $route.name === item.name}<span class="sidebar__active-dot" aria-hidden="true"></span>{/if}
        </a>
      {/each}
    </div>

    <div class="sidebar__group">
      <span class="sidebar__group-label">Operate</span>
      {#each operateRoutes as item (item.name)}
        <a href={item.hash} class="sidebar__item" class:sidebar__item--active={$route.name === item.name}>
          <span class="sidebar__icon">{@html item.icon}</span>
          <span class="sidebar__label">{item.label}</span>
          {#if $route.name === item.name}<span class="sidebar__active-dot" aria-hidden="true"></span>{/if}
        </a>
      {/each}
    </div>
  </div>

  <div class="sidebar__footer">
    {#each systemRoutes as item (item.name)}
      <a href={item.hash} class="sidebar__item" class:sidebar__item--active={$route.name === item.name}>
        <span class="sidebar__icon">{@html item.icon}</span>
        <span class="sidebar__label">{item.label}</span>
        {#if $route.name === item.name}<span class="sidebar__active-dot" aria-hidden="true"></span>{/if}
      </a>
    {/each}
    <span class="sidebar__alpha"><span></span> Alpha preview</span>
  </div>
</nav>

<style>
  .sidebar {
    position: sticky;
    top: 0;
    height: 100vh;
    flex-direction: column;
    width: 236px;
    flex-shrink: 0;
    padding: 1rem 0.85rem;
    background: var(--sidebar);
    color: var(--sidebar-ink);
    overflow-y: auto;
    box-shadow: 10px 0 36px rgb(18 22 39 / 0.08);
  }
  .sidebar__brand {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 0.35rem 0.55rem 1.35rem;
    color: var(--sidebar-ink);
    text-decoration: none;
  }
  .sidebar__brand-mark {
    width: 36px;
    height: 36px;
    border-radius: 10px;
    display: flex;
    align-items: flex-end;
    justify-content: center;
    gap: 3px;
    padding: 8px;
    background: linear-gradient(145deg, #a6b2ff, #5269e8);
    box-shadow: 0 8px 24px rgb(82 105 232 / 0.26);
  }
  .sidebar__brand-mark span {
    width: 4px;
    border-radius: 3px;
    background: #141722;
  }
  .sidebar__brand-mark span:nth-child(1) { height: 10px; opacity: 0.65; }
  .sidebar__brand-mark span:nth-child(2) { height: 18px; }
  .sidebar__brand-mark span:nth-child(3) { height: 14px; opacity: 0.82; }
  .sidebar__brand-copy {
    display: flex;
    flex-direction: column;
    min-width: 0;
  }
  .sidebar__brand-copy strong {
    font-size: 1.03rem;
    letter-spacing: -0.02em;
  }
  .sidebar__brand-copy small {
    margin-top: 0.08rem;
    color: var(--sidebar-muted);
    font-size: 0.68rem;
  }
  .sidebar__nav {
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }
  .sidebar__group {
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
  }
  .sidebar__group-label {
    padding: 0 0.7rem 0.35rem;
    color: var(--sidebar-muted);
    font-family: var(--font-mono);
    font-size: 0.61rem;
    font-weight: 500;
    text-transform: uppercase;
    letter-spacing: 0.12em;
  }
  .sidebar__item {
    position: relative;
    display: flex;
    align-items: center;
    gap: 0.72rem;
    padding: 0.62rem 0.7rem;
    min-height: 42px;
    border-radius: 9px;
    color: var(--sidebar-muted);
    text-decoration: none;
    font-size: 0.84rem;
    font-weight: 500;
    transition: color 150ms ease, background 150ms ease, transform 150ms ease;
  }
  .sidebar__item:hover {
    color: var(--sidebar-ink);
    background: color-mix(in oklab, var(--sidebar-surface) 82%, transparent);
  }
  .sidebar__item--active {
    background: var(--sidebar-surface);
    color: var(--sidebar-ink);
    font-weight: 640;
    box-shadow: inset 0 0 0 1px rgb(255 255 255 / 0.035);
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
  .sidebar__active-dot {
    width: 5px;
    height: 5px;
    margin-left: auto;
    border-radius: 50%;
    background: #9aa9ff;
    box-shadow: 0 0 0 4px rgb(154 169 255 / 0.1);
  }
  .sidebar__footer {
    display: flex;
    flex-direction: column;
    gap: 0.45rem;
    margin-top: auto;
    padding-top: 1rem;
  }
  .sidebar__alpha {
    display: inline-flex;
    align-items: center;
    gap: 0.45rem;
    padding: 0.55rem 0.7rem 0;
    color: var(--sidebar-muted);
    font-family: var(--font-mono);
    font-size: 0.62rem;
    text-transform: uppercase;
    letter-spacing: 0.08em;
  }
  .sidebar__alpha span {
    width: 5px;
    height: 5px;
    border-radius: 50%;
    background: #e7a416;
  }
</style>
