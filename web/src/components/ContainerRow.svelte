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
  import { Tween } from 'svelte/motion';
  import { cubicOut } from 'svelte/easing';
  import { prefersReducedMotion } from 'svelte/motion';
  import { live } from '../lib/sse.svelte';
  import { liveRing } from '../lib/livering.svelte';
  import { fmtBytes, fmtDuration, fmtPct, fmtRate } from '../lib/format';
  import { containerHealthStatus } from '../lib/containerStatus';
  import HealthDot from './HealthDot.svelte';
  import Sparkline from './Sparkline.svelte';

  const TWEEN_MS = 400;

  // registerSeedTarget (additive, optional -- live-seed history):
  // Containers.svelte fetches every visible row's cpu.pct history itself
  // (one AbortController, concurrency-capped, for the whole view -- see
  // its own doc) and delivers each row's own result through a callback
  // this row registers on mount, rather than through a prop -- see
  // registerSeedTarget's own doc in Containers.svelte for why a plain
  // reactive prop is the wrong shape for this specifically, at 23-rows
  // scale. Optional/absent in the collapsed Stopped section, which never
  // registers a target (this row's sparkline just builds up live-only
  // there, same as before this feature).
  let { name, registerSeedTarget = undefined } = $props();

  let cpuRing = liveRing((f) => f.containers[name]?.metrics['cpu.pct']);

  // Registers once: registerSeedTarget is a stable function reference
  // (Containers.svelte never reassigns it), so this effect's only
  // dependency never changes after the first run -- one registration
  // per row for its whole mounted lifetime, torn down via the returned
  // cleanup on unmount (a name that starts back up under the same
  // component instance -- doesn't happen here; Containers.svelte's own
  // keyed {#each} recreates the row -- but cleanup is still correct
  // regardless).
  $effect(() => {
    if (!registerSeedTarget) return;
    return registerSeedTarget(name, (points) => cpuRing.seed(points));
  });

  let c = $derived(live.frame?.containers?.[name]);
  let m = $derived(c?.metrics ?? {});
  let ts = $derived(live.frame?.ts ?? 0);
  let gpuPct = $derived((m['gpu.video.busy_pct'] ?? 0) + (m['gpu.render.busy_pct'] ?? 0));

  // Tweened numbers (mechanism 3, smooth-streaming): every cpu/mem/net/io
  // cell eases toward its newest value over TWEEN_MS instead of snapping
  // every 2s -- independent of the CPU column's own Sparkline, which
  // gets its OWN glide/ease from Sparkline's live mode (mechanisms 1+2).
  // ContainerRow is already its own component (one per container name,
  // kept stable by Containers.svelte's keyed {#each}), so a plain Tween
  // declared here persists across ticks the same way cpuRing already
  // does -- no extra per-row wrapper needed for these, unlike TopBarRow/
  // ArrayCardPoolRow, which exist because THEIR parents don't already
  // instantiate a component per row.
  function tweenTo(tween, value) {
    tween.set(value, { duration: prefersReducedMotion.current ? 0 : TWEEN_MS, easing: cubicOut });
  }

  let cpuTween = new Tween(0, { duration: TWEEN_MS, easing: cubicOut });
  let memBytesTween = new Tween(0, { duration: TWEEN_MS, easing: cubicOut });
  let memPctTween = new Tween(0, { duration: TWEEN_MS, easing: cubicOut });
  let netRxTween = new Tween(0, { duration: TWEEN_MS, easing: cubicOut });
  let netTxTween = new Tween(0, { duration: TWEEN_MS, easing: cubicOut });
  let ioReadTween = new Tween(0, { duration: TWEEN_MS, easing: cubicOut });
  let ioWriteTween = new Tween(0, { duration: TWEEN_MS, easing: cubicOut });

  $effect(() => tweenTo(cpuTween, m['cpu.pct'] ?? 0));
  $effect(() => tweenTo(memBytesTween, m['mem.bytes'] ?? 0));
  $effect(() => tweenTo(memPctTween, m['mem.pct'] ?? 0));
  $effect(() => tweenTo(netRxTween, m['net.rx_bps'] ?? 0));
  $effect(() => tweenTo(netTxTween, m['net.tx_bps'] ?? 0));
  $effect(() => tweenTo(ioReadTween, m['io.read_bps'] ?? 0));
  $effect(() => tweenTo(ioWriteTween, m['io.write_bps'] ?? 0));
</script>

{#if c}
  <tr class="container-row">
    <td><HealthDot status={containerHealthStatus(c.state, c.health)} /></td>
    <td class="container-row__name-cell">
      <a href={`#/containers/${encodeURIComponent(name)}`}>{name}</a>
    </td>
    <td class="container-row__cpu-cell">
      <span class="tabular-nums">{fmtPct(cpuTween.current)}</span>
      <Sparkline points={cpuRing.points} />
    </td>
    <td class="tabular-nums container-row__nowrap">
      {fmtBytes(memBytesTween.current)}
      {#if m['mem.pct'] !== undefined}<span class="container-row__muted">({fmtPct(memPctTween.current)})</span>{/if}
    </td>
    <td class="tabular-nums container-row__nowrap container-row__stacked">
      <span>&darr; {fmtRate(netRxTween.current)}</span>
      <span class="container-row__muted">&uarr; {fmtRate(netTxTween.current)}</span>
    </td>
    <td class="tabular-nums container-row__nowrap container-row__stacked">
      <span>r {fmtRate(ioReadTween.current)}</span>
      <span class="container-row__muted">w {fmtRate(ioWriteTween.current)}</span>
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
