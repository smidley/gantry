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
  import { cubicOut, linear } from 'svelte/easing';
  import { prefersReducedMotion } from 'svelte/motion';
  import { live } from '../lib/sse.svelte';
  import { liveRing } from '../lib/livering.svelte';
  import { fmtBytes, fmtDuration, fmtPct, fmtRate } from '../lib/format';
  import { containerHealthStatus } from '../lib/containerStatus';
  import { nearestPointAt } from '../lib/scrub';
  import { scrubBus } from '../lib/scrubbus.svelte';
  import ContainerIcon from './ContainerIcon.svelte';
  import HealthDot from './HealthDot.svelte';
  import Sparkline from './Sparkline.svelte';

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
  //
  // showState (additive, optional): the collapsed "Not running" section
  // now mixes stopped (exited/dead) and created (never-started)
  // containers together -- their health dots alone differ only by color
  // (serious vs. warning), so that section passes this to also print the
  // real state word, same text the mobile card list already shows.
  //
  // selected/onToggleSelect (additive -- multi detail view): the compare
  // checkbox, one per row. Containers.svelte owns the actual selection
  // Set; this row just reflects/toggles its own membership in it, same
  // "parent owns the state, row is a dumb reflection of one slice of it"
  // shape registerSeedTarget already uses for seeding.
  let { name, registerSeedTarget = undefined, showState = false, selected = false, onToggleSelect } = $props();

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

  // Tweened numbers (mechanism 3, perpetual-glide motion pass): every
  // cpu/mem/net/io cell glides toward its newest value over the shared
  // driver's own measured cadence (live.glideMs, linear curve) instead of
  // a fixed guessed duration and a front-loaded curve -- see
  // streamdriver.ts's "Cadence-driven glide" doc -- independent of the
  // CPU column's own Sparkline, which gets its OWN glide from
  // Sparkline's live mode (mechanisms 1+2). ContainerRow is already its
  // own component (one per container name, kept stable by
  // Containers.svelte's keyed {#each}), so a plain Tween declared here
  // persists across ticks the same way cpuRing already does -- no extra
  // per-row wrapper needed for these, unlike TopBarRow, which exists
  // because ITS parent doesn't already instantiate a component per row.
  function tweenTo(tween, value) {
    tween.set(value, { duration: prefersReducedMotion.current ? 0 : live.glideMs, easing: linear });
  }

  const SCRUB_TWEEN_MS = 120;

  let cpuTween = new Tween(0, { duration: live.glideMs, easing: linear });
  let memBytesTween = new Tween(0, { duration: live.glideMs, easing: linear });
  let memPctTween = new Tween(0, { duration: live.glideMs, easing: linear });
  let netRxTween = new Tween(0, { duration: live.glideMs, easing: linear });
  let netTxTween = new Tween(0, { duration: live.glideMs, easing: linear });
  let ioReadTween = new Tween(0, { duration: live.glideMs, easing: linear });
  let ioWriteTween = new Tween(0, { duration: live.glideMs, easing: linear });

  // cpuScrubHit (hover-scrub, synced): non-null whenever the shared bus
  // has a published ts, regardless of whether THIS row's own sparkline
  // is the one being hovered -- Scott's own requirement is that
  // scrubbing any one metric auto-scrubs every related one, and every
  // row on this page is "related" (same page, same instant), so every
  // row reads the SAME bus and finds ITS OWN cpu value at that instant.
  // cpuTween's effect below then eases toward that instead of the live
  // one, at the faster scrub-follow duration, exactly like StatTile's
  // own hero number (see its doc for the identical shape). Every other
  // cell in this row stays live-only; only the CPU cell has a sparkline.
  let cpuScrubHit = $derived(scrubBus.ts === null ? null : nearestPointAt(cpuRing.points, scrubBus.ts));

  $effect(() => {
    const reduced = prefersReducedMotion.current;
    if (cpuScrubHit) {
      cpuTween.set(cpuScrubHit.value, { duration: reduced ? 0 : SCRUB_TWEEN_MS, easing: cubicOut });
    } else {
      cpuTween.set(m['cpu.pct'] ?? 0, { duration: reduced ? 0 : live.glideMs, easing: linear });
    }
  });

  $effect(() => tweenTo(memBytesTween, m['mem.bytes'] ?? 0));
  $effect(() => tweenTo(memPctTween, m['mem.pct'] ?? 0));
  $effect(() => tweenTo(netRxTween, m['net.rx_bps'] ?? 0));
  $effect(() => tweenTo(netTxTween, m['net.tx_bps'] ?? 0));
  $effect(() => tweenTo(ioReadTween, m['io.read_bps'] ?? 0));
  $effect(() => tweenTo(ioWriteTween, m['io.write_bps'] ?? 0));
</script>

{#if c}
  <tr class="container-row">
    <td class="container-row__select-cell">
      <input
        type="checkbox"
        class="container-row__select"
        checked={selected}
        onchange={onToggleSelect}
        aria-label={`Compare ${name}`}
      />
    </td>
    <td><HealthDot status={containerHealthStatus(c.state, c.health)} label={showState ? c.state : undefined} /></td>
    <td class="container-row__name-cell">
      <a href={`#/containers/${encodeURIComponent(name)}`}>
        <ContainerIcon {name} icon={c.icon} size={20} />
        <span class="container-row__name-stack">
          <span class="container-row__name-text">{name}</span>
          {#if c.compose_project}
            <span class="container-row__compose-tag">{c.compose_project}</span>
          {/if}
        </span>
      </a>
    </td>
    <td class="container-row__cpu-cell">
      <span class="tabular-nums">{fmtPct(cpuTween.current)}</span>
      <Sparkline points={cpuRing.points} height={46} />
    </td>
    <td class="tabular-nums container-row__nowrap container-row__num">
      {fmtBytes(memBytesTween.current)}
      {#if m['mem.pct'] !== undefined}<span class="container-row__muted">({fmtPct(memPctTween.current)})</span>{/if}
    </td>
    <td class="tabular-nums container-row__nowrap container-row__num">
      <div class="container-row__stacked">
        <span>&darr; {fmtRate(netRxTween.current)}</span>
        <span class="container-row__muted">&uarr; {fmtRate(netTxTween.current)}</span>
      </div>
    </td>
    <td class="tabular-nums container-row__nowrap container-row__num">
      <div class="container-row__stacked">
        <span>r {fmtRate(ioReadTween.current)}</span>
        <span class="container-row__muted">w {fmtRate(ioWriteTween.current)}</span>
      </div>
    </td>
    <td class="tabular-nums container-row__num">{gpuPct > 0 ? fmtPct(gpuPct) : ''}</td>
    <td class="tabular-nums container-row__num">{m['pids'] !== undefined ? Math.round(m['pids']) : ''}</td>
    <td class="tabular-nums container-row__nowrap container-row__num">
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
    /* Column-width jitter fix: Containers.svelte's <colgroup> now owns
       every column's actual width (table-layout:fixed) -- a cell's own
       content never grows the column, so anything that can run long
       (the name link, in particular) must clip with an ellipsis instead
       of forcing an overflow. */
    overflow: hidden;
  }
  .container-row__select-cell {
    text-align: center;
  }
  /* Quiet -- native checkbox, no custom widget: accent-color is the only
     override, tying its checked state to the same series-1 hue the rest
     of the app already treats as "the" interactive accent (focus rings,
     active segmented-control tabs). */
  .container-row__select {
    accent-color: var(--series-1);
    cursor: pointer;
  }
  .container-row__name-cell a {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    color: var(--ink);
    text-decoration: none;
    font-weight: 500;
    overflow: hidden;
  }
  .container-row__name-cell a:hover {
    text-decoration: underline;
  }
  /* Stacks the name over its own compose-project tag (present only for a
     row whose container carries a com.docker.compose.project label) --
     a second, smaller line rather than crowding the tag onto the name's
     own line, which the fixed-width name column has little room for. */
  .container-row__name-stack {
    display: flex;
    flex-direction: column;
    min-width: 0;
    overflow: hidden;
  }
  .container-row__name-text {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    min-width: 0; /* lets the flex child actually shrink+ellipsis instead of pushing the fixed column wider */
  }
  .container-row__compose-tag {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--ink-2);
    font-family: var(--font-mono);
    font-size: 0.68rem;
    font-weight: 400;
  }
  .container-row__cpu-cell {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 0.5rem;
  }
  .container-row__cpu-cell :global(.sparkline) {
    /* 170px -> 220px, Scott: "too small to see the correct detail
       level" -- ContainerRow's height={46} above needs the wider track
       to still read as one continuous line rather than a cramped
       zigzag; Containers.svelte's own colgroup cpu width grows to match. */
    width: 220px;
    flex-shrink: 0;
  }
  .container-row__nowrap {
    white-space: nowrap;
  }
  .container-row__num {
    text-align: right;
  }
  .container-row__stacked {
    display: flex;
    flex-direction: column;
    align-items: flex-end;
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
