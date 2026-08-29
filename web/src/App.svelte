<!--
  App: the route table. Every Phase 3 view (Overview through Settings,
  Tasks 14-20) renders its real component; only the unmatched fallback
  ("not-found") still renders a placeholder <h1>.
-->
<script>
  import { onMount } from 'svelte';
  import Layout from './components/Layout.svelte';
  import { route } from './lib/router';
  import { live } from './lib/sse.svelte';

  import Overview from './views/Overview.svelte';
  import Containers from './views/Containers.svelte';
  import ContainerDetail from './views/ContainerDetail.svelte';
  import Compare from './views/Compare.svelte';
  import TopConsumers from './views/TopConsumers.svelte';
  import Storage from './views/Storage.svelte';
  import Maintenance from './views/Maintenance.svelte';
  import GPU from './views/GPU.svelte';
  import Events from './views/Events.svelte';
  import Settings from './views/Settings.svelte';

  onMount(() => {
    live.connect();
    return () => live.disconnect();
  });

  const ROUTE_TITLES = {
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
  {:else if $route.name === 'compare'}
    <Compare names={$route.params.names} />
  {:else if $route.name === 'top'}
    <TopConsumers initialResource={$route.params.resource} />
  {:else if $route.name === 'storage'}
    <Storage />
  {:else if $route.name === 'maintenance'}
    <Maintenance />
  {:else if $route.name === 'gpu'}
    <GPU />
  {:else if $route.name === 'events'}
    <Events />
  {:else if $route.name === 'settings'}
    <Settings />
  {:else}
    <h1>{ROUTE_TITLES[$route.name] ?? ROUTE_TITLES['not-found']}</h1>
    <p class="microlabel">View content lands in a later task.</p>
  {/if}
</Layout>
