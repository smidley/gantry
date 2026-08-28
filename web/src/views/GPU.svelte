<!--
  GPU: per-GPU-entity engine utilization (one GPUEntityCard per entity,
  sharing one page-level range picker), a container attribution table
  (per-engine busy % + total, live -- straight off the live frame, no
  fetch), an Nvidia VRAM section when any container carries it, and a
  PSI-tier hint when pressure insight isn't available on this box.
-->
<script>
  import { live } from '../lib/sse.svelte';
  import { fmtBytes, fmtPct } from '../lib/format';
  import { enginesPresent, GPU_ENGINE_ORDER, GPU_ENTITY_ENGINE_ORDER } from '../lib/metrics';
  import GPUEntityCard from '../components/GPUEntityCard.svelte';

  const SYNC_KEY = 'gpu-view';
  const RANGES = [
    { key: 'live', label: 'Live · 15m' },
    { key: '1h', label: '1h' },
    { key: '24h', label: '24h' },
    { key: '7d', label: '7d' },
    { key: '30d', label: '30d' },
  ];
  const ENGINE_COLUMN_LABEL = { render: 'Render', video: 'Video', 'video-enhance': 'Video enhance', copy: 'Copy' };

  let activeRange = $state('live');

  let sources = $derived(live.frame?.sources ?? {});
  let gpuEntities = $derived(Object.entries(live.frame?.gpu ?? {}));
  let gpuEntityNames = $derived(gpuEntities.map(([entity]) => entity).sort());

  // hasAnyEngineData gates the chart section: a gpu entity that exists
  // in the frame but carries none of the fixed engine keys (shouldn't
  // happen in practice -- collector.go/nvidia.go always write BOTH the
  // entity- and container-level series together -- but defensive rather
  // than assumed) counts the same as no entity at all.
  let hasAnyEngineData = $derived(
    gpuEntities.some(([, metrics]) => enginesPresent(metrics, (e) => `engine.${e}.busy_pct`, GPU_ENTITY_ENGINE_ORDER).length > 0),
  );

  // emptyHint: data presence (hasAnyEngineData) is what actually gates
  // rendering -- a source reporting "ok" or not is secondary context,
  // matching GPUStrip's own precedent of rendering off the frame's data
  // rather than off collector status (a fake-data box, or one where the
  // real collector is transiently unavailable but already has recorded
  // samples, still has real data worth showing). This hint is used only
  // once there's truly nothing to show, to explain WHY -- gpu's own
  // Detail preferred (the primary DRM path), falling back to nvidia's.
  let emptyHint = $derived(
    sources.gpu && sources.gpu !== 'ok' ? sources.gpu : sources.nvidia && sources.nvidia !== 'ok' ? sources.nvidia : null,
  );

  // Attribution table: every container with at least one gpu.<engine>.
  // busy_pct metric (GPU_ENGINE_ORDER -- NOT the entity variant: Nvidia's
  // v1 per-container data is VRAM only, never a "gpu.gpu.busy_pct" busy
  // percentage, so the container-attribution vocabulary never includes
  // the "gpu" pseudo-engine). A plain $derived, not effect-gated, so it
  // stays LIVE (recomputes every frame), per the view's own contract.
  let attributionRows = $derived.by(() => {
    const rows = [];
    for (const [name, c] of Object.entries(live.frame?.containers ?? {})) {
      const engines = enginesPresent(c.metrics, (e) => `gpu.${e}.busy_pct`);
      if (engines.length === 0) continue;
      const perEngine = {};
      let total = 0;
      for (const engine of GPU_ENGINE_ORDER) {
        const v = c.metrics[`gpu.${engine}.busy_pct`];
        if (v !== undefined) {
          perEngine[engine] = v;
          total += v;
        }
      }
      rows.push({ name, perEngine, total });
    }
    return rows.sort((a, b) => (b.total !== a.total ? b.total - a.total : a.name.localeCompare(b.name)));
  });

  // Nvidia VRAM section: gated on DATA PRESENCE (any container actually
  // carrying gpu.nvidia.mem_mib), not on sources.nvidia's current
  // status -- a transient source flap shouldn't hide already-known VRAM
  // figures, and this is the more directly testable, unambiguous signal.
  let nvidiaRows = $derived.by(() =>
    Object.entries(live.frame?.containers ?? {})
      .filter(([, c]) => c.metrics['gpu.nvidia.mem_mib'] !== undefined)
      .map(([name, c]) => ({ name, memMiB: c.metrics['gpu.nvidia.mem_mib'] }))
      .sort((a, b) => (b.memMiB !== a.memMiB ? b.memMiB - a.memMiB : a.name.localeCompare(b.name))),
  );

  let pressureDetail = $derived(sources.pressure);
  let showPsiHint = $derived(pressureDetail !== undefined && pressureDetail !== 'ok');
</script>

<div class="gpu-view">
  <h1 class="page-title">GPU</h1>

  {#if !hasAnyEngineData}
    <div class="card gpu-view__empty">
      <p class="gpu-view__empty-title">No GPU activity detected yet.</p>
      {#if emptyHint}
        <p class="microlabel gpu-view__empty-hint">{emptyHint}</p>
      {/if}
    </div>
  {:else}
    <div class="gpu-view__range-picker" role="group" aria-label="Time range">
      {#each RANGES as r (r.key)}
        <button
          type="button"
          class="gpu-view__range-btn"
          class:gpu-view__range-btn--active={activeRange === r.key}
          onclick={() => (activeRange = r.key)}
        >
          {r.label}
        </button>
      {/each}
    </div>
    <div class="gpu-view__entities">
      {#each gpuEntityNames as entity (entity)}
        <GPUEntityCard {entity} {activeRange} syncKey={SYNC_KEY} />
      {/each}
    </div>
  {/if}

  <div class="card gpu-view__attribution">
    <span class="microlabel">Container attribution</span>
    {#if attributionRows.length === 0}
      <p class="microlabel gpu-view__attribution-empty">No containers are using the GPU right now.</p>
    {:else}
      <div class="gpu-view__table-wrap">
        <table class="gpu-view__table">
          <thead>
            <tr>
              <th class="microlabel">Name</th>
              {#each GPU_ENGINE_ORDER as engine (engine)}
                <th class="microlabel">{ENGINE_COLUMN_LABEL[engine]}</th>
              {/each}
              <th class="microlabel">Total</th>
            </tr>
          </thead>
          <tbody>
            {#each attributionRows as row (row.name)}
              <tr>
                <td><a href={`#/containers/${encodeURIComponent(row.name)}`}>{row.name}</a></td>
                {#each GPU_ENGINE_ORDER as engine (engine)}
                  <td class="tabular-nums">{row.perEngine[engine] !== undefined ? fmtPct(row.perEngine[engine]) : '—'}</td>
                {/each}
                <td class="tabular-nums">{fmtPct(row.total)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
    {#if showPsiHint}
      <p class="microlabel gpu-view__psi-hint">
        Pressure insight unavailable: {pressureDetail}
        <a class="gpu-view__psi-learn-more" href="https://github.com/smidley/gantry/blob/main/docs/psi.md" target="_blank" rel="noopener">
          Learn more &rarr;
        </a>
      </p>
    {/if}
  </div>

  {#if nvidiaRows.length > 0}
    <div class="card gpu-view__nvidia">
      <span class="microlabel">Nvidia VRAM</span>
      <div class="gpu-view__table-wrap">
        <table class="gpu-view__table">
          <thead>
            <tr>
              <th class="microlabel">Name</th>
              <th class="microlabel">VRAM</th>
            </tr>
          </thead>
          <tbody>
            {#each nvidiaRows as row (row.name)}
              <tr>
                <td><a href={`#/containers/${encodeURIComponent(row.name)}`}>{row.name}</a></td>
                <td class="tabular-nums">{fmtBytes(row.memMiB * 1024 * 1024)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </div>
  {/if}
</div>

<style>
  .gpu-view {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }
  .gpu-view__empty {
    padding: 2rem 1rem;
    text-align: center;
  }
  .gpu-view__empty-title {
    margin: 0 0 0.4rem 0;
  }
  .gpu-view__empty-hint {
    margin: 0;
  }
  .gpu-view__range-picker {
    display: flex;
    gap: 0.4rem;
    flex-wrap: wrap;
  }
  .gpu-view__range-btn {
    min-height: 40px;
    padding: 0 0.85rem;
    border-radius: 6px;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: transparent;
    color: var(--ink-2);
    font-size: 0.82rem;
    cursor: pointer;
  }
  .gpu-view__range-btn--active {
    background: color-mix(in oklab, var(--series-1) 15%, transparent);
    border-color: var(--series-1);
    color: var(--series-1);
    font-weight: 500;
  }
  .gpu-view__entities {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }
  .gpu-view__attribution,
  .gpu-view__nvidia {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .gpu-view__attribution-empty {
    margin: 0;
  }
  .gpu-view__table-wrap {
    overflow-x: auto;
  }
  .gpu-view__table {
    width: 100%;
    border-collapse: collapse;
    min-width: 30rem;
  }
  .gpu-view__table th {
    text-align: left;
    padding: 0.4rem 0.6rem;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 12%, transparent);
    white-space: nowrap;
  }
  .gpu-view__table td {
    padding: 0.4rem 0.6rem;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 6%, transparent);
    font-size: 0.85rem;
    white-space: nowrap;
  }
  .gpu-view__table td a {
    color: var(--ink);
    text-decoration: none;
    font-weight: 500;
  }
  .gpu-view__table td a:hover {
    text-decoration: underline;
  }
  .gpu-view__psi-hint {
    margin: 0;
  }
  .gpu-view__psi-learn-more {
    margin-left: 0.4em;
    color: var(--series-1);
    text-decoration: none;
    white-space: nowrap;
  }
  .gpu-view__psi-learn-more:hover {
    text-decoration: underline;
  }
</style>
