<script lang="ts">
  import type { Component } from 'svelte';
  import type { Route } from '../lib/router';
  import { routeContent } from '../lib/navigation';
  import LoadingState from './LoadingState.svelte';

  let { current }: { current: Route } = $props();
  const loaders: Record<string, () => Promise<{ default: Component<any> }>> = {
    overview: () => import('../views/Overview.svelte'),
    containers: () => import('../views/Containers.svelte'),
    'container-detail': () => import('../views/ContainerDetail.svelte'),
    compare: () => import('../views/Compare.svelte'),
    top: () => import('../views/TopConsumers.svelte'),
    storage: () => import('../views/Storage.svelte'),
    maintenance: () => import('../views/Maintenance.svelte'),
    gpu: () => import('../views/GPU.svelte'),
    events: () => import('../views/Events.svelte'),
    insights: () => import('../views/Insights.svelte'),
    'insight-detail': () => import('../views/InsightDetail.svelte'),
    alerts: () => import('../views/Alerts.svelte'),
    settings: () => import('../views/Settings.svelte'),
  };
  let loader = $derived(loaders[current.name]);
  let view = $derived(loader?.());
  let viewProps = $derived({
    name: current.params.name,
    names: current.params.names,
    id: current.params.id,
    mode: current.params.mode,
    initialState: current.params.state,
    initialResource: current.params.resource,
  });
</script>

{#if view}
  {#await view}
    <LoadingState title="Loading view" detail="Your dashboard will appear shortly." />
  {:then module}
    {@const View = module.default}
    <div use:routeContent={current}><View {...viewProps} /></div>
  {:catch}
    <div use:routeContent={current}>
      <h1>This view could not load</h1>
      <p>Check your connection and reload Gantry to try again.</p>
      <button class="btn" onclick={() => location.reload()}>Reload</button>
    </div>
  {/await}
{:else}
  <div use:routeContent={current}><h1>Page not found</h1><p><a href="#/">Return to Overview</a></p></div>
{/if}
