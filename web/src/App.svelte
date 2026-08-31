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
  import { alertRules } from './lib/alertRules.svelte';

  import Overview from './views/Overview.svelte';
  import Containers from './views/Containers.svelte';
  import ContainerDetail from './views/ContainerDetail.svelte';
  import Compare from './views/Compare.svelte';
  import TopConsumers from './views/TopConsumers.svelte';
  import Storage from './views/Storage.svelte';
  import Maintenance from './views/Maintenance.svelte';
  import GPU from './views/GPU.svelte';
  import Events from './views/Events.svelte';
  import Insights from './views/Insights.svelte';
  import Alerts from './views/Alerts.svelte';
  import Settings from './views/Settings.svelte';
  import LoadingState from './components/LoadingState.svelte';

  const LIVE_ROUTES = new Set(['overview', 'containers', 'container-detail', 'compare', 'top', 'storage', 'gpu']);

  onMount(() => {
    live.connect();
    return () => live.disconnect();
  });

  // Band unification (Task 12): one boot fetch of the current alert
  // rules, so thresholds.ts's band() reads the SAME numbers the alert
  // engine fires and clears on from the very first render -- see
  // alertRules.svelte.ts's own doc for why this is the only place that
  // ever calls ensureLoaded() at boot (the rule editor's own save()
  // re-derives the band table itself, on every successful PUT).
  onMount(() => {
    alertRules.ensureLoaded();
  });

  const ROUTE_TITLES = {
    'not-found': 'Not found',
  };
</script>

<Layout>
  {#if LIVE_ROUTES.has($route.name) && !live.frame}
    <LoadingState title="Connecting to your server" detail="The first live system snapshot will appear here automatically." />
  {:else if $route.name === 'overview'}
    <Overview />
  {:else if $route.name === 'containers'}
    <Containers initialState={$route.params.state} />
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
  {:else if $route.name === 'insights'}
    <Insights mode={$route.params.mode} />
  {:else if $route.name === 'alerts'}
    <Alerts />
  {:else if $route.name === 'settings'}
    <Settings />
  {:else}
    <h1>{ROUTE_TITLES[$route.name] ?? ROUTE_TITLES['not-found']}</h1>
    <p class="microlabel">View content lands in a later task.</p>
  {/if}
</Layout>
