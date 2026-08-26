<!--
  App: the route table. Each of the 8 routes renders a placeholder <h1>
  for now -- the 8 real views are later tasks (14-20); this just proves
  the shell is navigable end to end. Overview additionally wires ONE
  StatTile to the live store's host cpu.total as the SSE-to-UI proof.
-->
<script>
  import { onMount } from 'svelte';
  import Layout from './components/Layout.svelte';
  import StatTile from './components/StatTile.svelte';
  import { route } from './lib/router';
  import { live } from './lib/sse.svelte';
  import { fmtPct } from './lib/format';

  onMount(() => {
    live.connect();
    return () => live.disconnect();
  });

  const ROUTE_TITLES = {
    overview: 'Overview',
    containers: 'Containers',
    'container-detail': 'Container',
    top: 'Top Consumers',
    storage: 'Storage',
    gpu: 'GPU',
    events: 'Events',
    settings: 'Settings',
    'not-found': 'Not found',
  };
</script>

<Layout>
  {#if $route.name === 'overview'}
    <h1>Overview</h1>
    <StatTile
      label="Host CPU"
      value={fmtPct(live.frame?.host?.['cpu.total'] ?? 0)}
      status={live.connected ? 'good' : undefined}
    />
  {:else}
    <h1>{ROUTE_TITLES[$route.name] ?? ROUTE_TITLES['not-found']}</h1>
    <p class="microlabel">View content lands in a later task.</p>
  {/if}
</Layout>
