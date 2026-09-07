<script>
  import { onMount } from 'svelte';
  import Layout from './components/Layout.svelte';
  import { route } from './lib/router';
  import { live } from './lib/sse.svelte';
  import { alertRules } from './lib/alertRules.svelte';
  import { auth } from './lib/auth.svelte';

  import RouteView from './components/RouteView.svelte';
  import { installNavigation } from './lib/navigation';
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
    const cleanup = installNavigation();
    return () => { live.disconnect(); cleanup(); };
  });

  $effect(() => {
    if (!auth.ready) return;
    if (auth.error || auth.needsSetup || auth.needsLogin) {
      live.disconnect();
    } else {
      live.connect();
      alertRules.ensureLoaded();
    }
  });

</script>

{#if !auth.ready}
  <!-- Nothing gate-dependent renders before the boot status answer: a
       locked box must never flash the dashboard shell. -->
{:else if auth.error}
  <main class="auth-unavailable"><h1>Unable to connect</h1><p role="alert">{auth.error}</p><button class="btn" onclick={() => auth.refresh()}>Try again</button></main>
{:else if auth.needsSetup}
  <Setup />
{:else if auth.needsLogin}
  <Login />
{:else}
<Layout>
  {#if LIVE_ROUTES.has($route.name) && !live.frame}
    <LoadingState title="Connecting to your server" detail="The first live system snapshot will appear here automatically." />
  {:else}
    {#key $route.name + ($route.params.name ?? $route.params.id ?? $route.params.resource ?? '')}
      <RouteView current={$route} />
    {/key}
  {/if}
</Layout>
{/if}

<style>
 .auth-unavailable { max-width: 36rem; margin: 15vh auto; padding: 1.5rem; }
</style>
