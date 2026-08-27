<!--
  ContainerRow: one <tr> in the Containers table -- its own component so
  it can call liveRing (a $state/$effect pair) once per row during ITS
  OWN initialization, which is what a per-container live CPU sparkline
  needs: calling a runes-using helper from inside a {#each} block in the
  PARENT's script wouldn't have the right timing, but a child component
  instantiated per list item does.

  Renders nothing once its container has left the live frame (removal is
  reflected on the next sort-triggering effect in Containers.svelte,
  which drops the name from the list entirely; this is just the harmless
  gap between "frame lost it" and "the list re-sorts").
-->
<script>
  import { live } from '../lib/sse.svelte';
  import { liveRing } from '../lib/livering.svelte';
  import { fmtBytes, fmtDuration, fmtPct, fmtRate } from '../lib/format';
  import { containerHealthStatus } from '../lib/containerStatus';
  import HealthDot from './HealthDot.svelte';
  import Sparkline from './Sparkline.svelte';

  let { name } = $props();

  let cpuRing = liveRing((f) => f.containers[name]?.metrics['cpu.pct']);

  let c = $derived(live.frame?.containers?.[name]);
  let m = $derived(c?.metrics ?? {});
  let ts = $derived(live.frame?.ts ?? 0);
  let gpuPct = $derived((m['gpu.video.busy_pct'] ?? 0) + (m['gpu.render.busy_pct'] ?? 0));
</script>

{#if c}
  <tr class="container-row">
    <td><HealthDot status={containerHealthStatus(c.state, c.health)} /></td>
    <td class="container-row__name-cell">
      <a href={`#/containers/${encodeURIComponent(name)}`}>{name}</a>
    </td>
    <td class="container-row__cpu-cell">
      <span class="tabular-nums">{fmtPct(m['cpu.pct'] ?? 0)}</span>
      <Sparkline points={cpuRing.points} />
    </td>
    <td class="tabular-nums container-row__nowrap">
      {fmtBytes(m['mem.bytes'] ?? 0)}
      {#if m['mem.pct'] !== undefined}<span class="container-row__muted">({fmtPct(m['mem.pct'])})</span>{/if}
    </td>
    <td class="tabular-nums container-row__nowrap container-row__stacked">
      <span>&darr; {fmtRate(m['net.rx_bps'] ?? 0)}</span>
      <span class="container-row__muted">&uarr; {fmtRate(m['net.tx_bps'] ?? 0)}</span>
    </td>
    <td class="tabular-nums container-row__nowrap container-row__stacked">
      <span>r {fmtRate(m['io.read_bps'] ?? 0)}</span>
      <span class="container-row__muted">w {fmtRate(m['io.write_bps'] ?? 0)}</span>
    </td>
    <td class="tabular-nums">{gpuPct > 0 ? fmtPct(gpuPct) : ''}</td>
    <td class="tabular-nums">{m['pids'] !== undefined ? Math.round(m['pids']) : ''}</td>
    <td class="tabular-nums container-row__nowrap">
      {m['meta.started_at'] !== undefined ? fmtDuration(ts - m['meta.started_at']) : '—'}
    </td>
    <td class="container-row__image-cell" title={c.image}>{c.image}</td>
  </tr>
{/if}

<style>
  .container-row td {
    padding: 0.5rem 0.6rem;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 6%, transparent);
    font-size: 0.82rem;
    vertical-align: middle;
  }
  .container-row__name-cell a {
    color: var(--ink);
    text-decoration: none;
    font-weight: 500;
  }
  .container-row__name-cell a:hover {
    text-decoration: underline;
  }
  .container-row__cpu-cell {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    min-width: 7rem;
  }
  .container-row__cpu-cell :global(.sparkline) {
    width: 60px;
    flex-shrink: 0;
  }
  .container-row__nowrap {
    white-space: nowrap;
  }
  .container-row__stacked {
    display: flex;
    flex-direction: column;
    line-height: 1.3;
  }
  .container-row__muted {
    color: var(--ink-2);
    font-size: 0.75rem;
  }
  .container-row__image-cell {
    max-width: 14rem;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--ink-2);
    font-family: var(--font-mono);
    font-size: 0.75rem;
  }
</style>
