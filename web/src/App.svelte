<!--
  App: the route table. Overview/Containers/Container-detail/Top
  Consumers/Storage (Tasks 14-18) render their real views; GPU/Events/
  Settings (later Tasks 19-20) still render a placeholder <h1> -- this
  proves the shell is navigable end to end for every route regardless of
  which task has landed.
-->
<script>
  import { onMount } from 'svelte';
  import Layout from './components/Layout.svelte';
  import { route } from './lib/router';
  import { live } from './lib/sse.svelte';

  import Overview from './views/Overview.svelte';
  import Containers from './views/Containers.svelte';
  import ContainerDetail from './views/ContainerDetail.svelte';
  import TopConsumers from './views/TopConsumers.svelte';
  import Storage from './views/Storage.svelte';

  onMount(() => {
    live.connect();
    return () => live.disconnect();
  });

  const ROUTE_TITLES = {
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
  {:else if $route.name === 'container-detail'}
    <!-- Keyed on the name param: navigating straight from one
         container's detail page to another's must fully reset every
         per-container piece of state (live rings, fetched series,
         events) rather than reusing the component instance with just a
         new prop value -- see ContainerDetail's own liveRing calls,
         which would otherwise keep accumulating points from BOTH
         containers into the same ring. -->
    {#key $route.params.name}
      <ContainerDetail name={$route.params.name} />
    {/key}
  {:else if $route.name === 'top'}
    <TopConsumers />
  {:else if $route.name === 'storage'}
    <Storage />
  {:else}
    <h1>{ROUTE_TITLES[$route.name] ?? ROUTE_TITLES['not-found']}</h1>
    <p class="microlabel">View content lands in a later task.</p>
  {/if}
</Layout>
