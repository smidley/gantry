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
  import { auth } from './lib/auth.svelte';

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
  import Login from './views/Login.svelte';
  import Setup from './views/Setup.svelte';
  import LoadingState from './components/LoadingState.svelte';

  const LIVE_ROUTES = new Set(['overview', 'containers', 'container-detail', 'compare', 'top', 'storage', 'gpu']);

  // Auth first: one boot status fetch decides setup screen vs login
  // screen vs app (see auth.svelte.ts). Everything that talks to the API
  // at boot -- the SSE connection, the alert-rules band fetch (Task 12's
  // own doc, unchanged otherwise) -- waits for the gate to be open and
  // tears down when it closes again (a 401 mid-session flips needsLogin,
  // e.g. a credential set from another browser, or an expired session):
  // a closed gate would just 401 the EventSource into a silent retry
  // loop.
  onMount(() => {
    auth.init();
    return () => live.disconnect();
  });

  $effect(() => {
    if (!auth.ready) return;
    if (auth.needsSetup || auth.needsLogin) {
      live.disconnect();
    } else {
      live.connect();
      alertRules.ensureLoaded();
    }
  });

  const ROUTE_TITLES = {
    'not-found': 'Not found',
  };
</script>

{#if !auth.ready}
  <!-- Nothing gate-dependent renders before the boot status answer: a
       locked box must never flash the dashboard shell. -->
{:else if auth.needsSetup}
  <Setup />
{:else if auth.needsLogin}
  <Login />
{:else}
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
{/if}
