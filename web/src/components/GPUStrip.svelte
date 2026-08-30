<!--
  GPUStrip: Overview's compact per-GPU-entity engine utilization mini
  bars, shown only when the live frame has at least one gpu entity.
  Engine color slots are fixed (render=series-1, video=series-2,
  video-enhance=series-3, copy=series-4), matching Task 19's GPU view --
  engines are a small, fixed vocabulary here (not a leaderboard whose
  membership churns), so a stable per-engine color is the right call,
  unlike TopBarList's single-hue leaderboard bars. GPU_ENTITY_ENGINE_ORDER
  (rather than plain GPU_ENGINE_ORDER) also recognizes the Nvidia v1
  path's solo "gpu" pseudo-engine (see metrics.ts's own doc) -- an
  Nvidia-only host's gpu entity has no render/video/video-enhance/copy
  keys at all, only "engine.gpu.busy_pct", which without this fallback
  this strip would silently never render a bar for.

  gpuMeta (additive, optional -- GPU card title fix: the entity id IS a
  raw PCI address for the DRM path, e.g. "0000:00:02.0" -- Scott's own
  question, "what does this mean?") backs each entity's own title via
  gpuTitle (metrics.ts): "Intel GPU (i915)" instead of the bare address,
  with the address itself demoted to a small secondary line rather than
  dropped outright. Absent meta (a snapshot that hasn't caught up yet)
  falls back to today's exact behavior -- just the bare entity id, no
  secondary line.
-->
<script>
  import { fmtPct } from '../lib/format';
  import { enginesPresent, GPU_ENTITY_ENGINE_ORDER, gpuTitle } from '../lib/metrics';

  let { gpu = {}, gpuMeta = {} } = $props();

  const SERIES_VAR = {
    render: '--series-1',
    video: '--series-2',
    'video-enhance': '--series-3',
    copy: '--series-4',
    gpu: '--series-1', // Nvidia's solo pseudo-engine -- never co-present with the other four, so slot reuse is harmless
  };

  let entities = $derived(
    Object.entries(gpu)
      .map(([entity, metrics]) => ({
        entity,
        engines: enginesPresent(metrics, (e) => `engine.${e}.busy_pct`, GPU_ENTITY_ENGINE_ORDER).map((engine) => ({
          engine,
          pct: metrics[`engine.${engine}.busy_pct`],
        })),
      }))
      .filter((e) => e.engines.length > 0)
      .sort((a, b) => a.entity.localeCompare(b.entity)),
  );
</script>

{#if entities.length > 0}
  <div class="card gpu-strip">
    <span class="microlabel">GPU</span>
    {#each entities as { entity, engines } (entity)}
      <div class="gpu-strip__entity">
        <div class="gpu-strip__entity-head">
          <span class="gpu-strip__entity-name">{gpuTitle(entity, gpuMeta[entity])}</span>
          {#if gpuMeta[entity]}<span class="microlabel gpu-strip__entity-id">{entity}</span>{/if}
        </div>
        <div class="gpu-strip__engines">
          {#each engines as { engine, pct } (engine)}
            <div class="gpu-strip__engine">
              <span class="microlabel gpu-strip__engine-label">{engine}</span>
              <div class="gpu-strip__track">
                <div
                  class="gpu-strip__fill"
                  style="width: {Math.min(100, Math.max(0, pct))}%; background: var({SERIES_VAR[engine]})"
                ></div>
              </div>
              <span class="tabular-nums gpu-strip__value">{fmtPct(pct)}</span>
            </div>
          {/each}
        </div>
      </div>
    {/each}
  </div>
{/if}

<style>
  .gpu-strip {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
  }
  .gpu-strip__entity {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
  }
  .gpu-strip__entity-head {
    display: flex;
    align-items: baseline;
    gap: 0.5rem;
  }
  .gpu-strip__entity-name {
    font-family: var(--font-mono);
    font-size: 0.8rem;
    color: var(--ink);
  }
  /* The raw PCI address, demoted -- see the module doc's own gpuMeta
     paragraph. */
  .gpu-strip__entity-id {
    font-size: 0.68rem;
  }
  .gpu-strip__engines {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
  }
  .gpu-strip__engine {
    display: grid;
    grid-template-columns: 5.5rem 1fr auto;
    align-items: center;
    gap: 0.5rem;
  }
  .gpu-strip__engine-label {
    color: var(--ink-2);
  }
  .gpu-strip__track {
    height: 6px;
    border-radius: 3px;
    background: color-mix(in oklab, var(--ink) 8%, transparent);
    overflow: hidden;
  }
  .gpu-strip__fill {
    height: 100%;
  }
  .gpu-strip__value {
    font-size: 0.75rem;
    min-width: 3em;
    text-align: right;
  }
</style>
