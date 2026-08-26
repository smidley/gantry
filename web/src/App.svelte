<!--
  App: the route table. Overview/Containers (Tasks 14-15) render their
  real views; Container-detail/Top Consumers (Tasks 16-17, this same SDD
  batch) and Storage/GPU/Events/Settings (later Tasks 18-20) still render
  a placeholder <h1> -- this proves the shell is navigable end to end for
  every route regardless of which task has landed.
-->
<script>
  import { onMount } from 'svelte';
  import Layout from './components/Layout.svelte';
  import { route } from './lib/router';
  import { live } from './lib/sse.svelte';

  import Overview from './views/Overview.svelte';
  import Containers from './views/Containers.svelte';

  onMount(() => {
    live.connect();
    return () => live.disconnect();
  });

  const ROUTE_TITLES = {
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
    <Overview />
  {:else if $route.name === 'containers'}
    <Containers />
  {:else}
    <h1>{ROUTE_TITLES[$route.name] ?? ROUTE_TITLES['not-found']}</h1>
    <p class="microlabel">View content lands in a later task.</p>
  {/if}
</Layout>
