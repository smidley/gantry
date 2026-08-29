<!--
  FleetStrip: D2's countable evidence for the headline's fleet sentence --
  one literal unit per container, quiet when it's running clean, enlarged
  and status-colored when it isn't (containerHealthStatus decides which).
  This is addition, not the compass-rose ring's proportional arc: reading
  "everything's fine" means seeing no enlarged marks at all, and reading
  "which one" means finding the one (or few) that stand out, never doing
  arithmetic on a sliver's angle.

  Every unit is its own link into that container's detail page -- not
  only the flagged ones, a strict superset of what the design calls for
  and one less special case in the markup -- and carries a real
  aria-label (name + state, and health only when it says something a
  screen reader wouldn't already infer from "running", mirroring
  containerHealthStatus's own health/state precedence), so the strip's
  full information survives with color entirely removed. Units are
  small by design (this is a density device, the fleet's own equivalent
  of a contribution graph), but never below the 8px floor a 38+
  container fleet still needs to stay individually tappable, and the
  strip wraps rather than ever forcing horizontal scroll.

  Heat ("make it earn its space"): a clean-running unit additionally
  tints by its own live CPU host-share (fleetHeatVar's single-hue seq
  ramp) -- status colors above always override it, stopped units never
  get it. Hover AND keyboard focus reveal a small label (icon+name+CPU+
  mem, CoreBudgetRibbon's own hover-label convention) since the strip
  otherwise carries this information in aria-label alone.
-->
<script>
  import { prefersReducedMotion } from 'svelte/motion';
  import { live as liveStore } from '../lib/sse.svelte';
  import { containerHealthStatus } from '../lib/containerStatus';
  import { fleetHeatVar } from '../lib/fleetHeat';
  import { fmtBytes, fmtPct } from '../lib/format';
  import ContainerIcon from './ContainerIcon.svelte';

  // containers: [{ name, state, health, icon?, cpuPct?, memBytes? }] --
  // cpuPct/memBytes are the live cpu.pct/mem.bytes samples (absent for a
  // container with no metrics yet), read straight off the live frame by
  // the caller.
  let { containers = [] } = $props();

  let glideMs = $derived(prefersReducedMotion.current ? 0 : liveStore.glideMs);

  let hoveredName = $state(null);
  let hoveredContainer = $derived(containers.find((c) => c.name === hoveredName) ?? null);

  function ariaLabel(name, state, health) {
    const meaningfulHealth = health === 'unhealthy' || health === 'starting';
    return meaningfulHealth ? `${name}: ${state}, ${health}` : `${name}: ${state}`;
  }
</script>

<div class="fleet-strip-wrap">
  <ul class="fleet-strip" aria-label={`Container fleet, ${containers.length} total`}>
    {#each containers as c (c.name)}
      {@const stopped = c.state !== 'running'}
      {@const status = containerHealthStatus(c.state, c.health)}
      {@const heat = !stopped && status === 'good' ? fleetHeatVar(c.cpuPct) : null}
      <li>
        <a
          class="fleet-unit"
          class:fleet-unit--stopped={stopped}
          class:fleet-unit--warning={!stopped && status === 'warning'}
          class:fleet-unit--serious={!stopped && status === 'serious'}
          class:fleet-unit--critical={!stopped && status === 'critical'}
          href={`#/containers/${encodeURIComponent(c.name)}`}
          aria-label={ariaLabel(c.name, c.state, c.health)}
          style={heat ? `--heat-bg: ${heat}; transition-duration: ${glideMs}ms` : undefined}
          onmouseenter={() => (hoveredName = c.name)}
          onmouseleave={() => (hoveredName = null)}
          onfocus={() => (hoveredName = c.name)}
          onblur={() => (hoveredName = null)}
        ></a>
      </li>
    {/each}
  </ul>
  <div class="fleet-strip__label" class:fleet-strip__label--visible={!!hoveredContainer}>
    {#if hoveredContainer}
      <ContainerIcon name={hoveredContainer.name} icon={hoveredContainer.icon} size={14} />
      <span>{hoveredContainer.name}</span>
      {#if hoveredContainer.cpuPct !== undefined}
        <span class="tabular-nums">{fmtPct(hoveredContainer.cpuPct)}</span>
      {/if}
      {#if hoveredContainer.memBytes !== undefined}
        <span class="tabular-nums">{fmtBytes(hoveredContainer.memBytes)}</span>
      {/if}
    {/if}
  </div>
</div>

<style>
  .fleet-strip-wrap {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
  }
  /* A plain <ul>/<li> pair -- not a <div role="list">/<a role="listitem">
     one -- carries list/listitem semantics implicitly and correctly: an
     explicit role="listitem" on the <a> itself is invalid (an
     interactive element can't take a non-interactive ARIA role) and
     Svelte's own a11y check rejects it. */
  .fleet-strip {
    display: flex;
    flex-wrap: wrap;
    gap: 2px;
    max-width: 100%;
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .fleet-strip li {
    display: flex;
  }
  .fleet-unit {
    display: block;
    width: 8px;
    height: 16px;
    border-radius: 1px;
    /* --heat-bg (inline, above): a var() indirection rather than setting
       `background` straight from the style attribute, so the plain
       :hover rule below (a normal stylesheet declaration for the same
       property) can still win by specificity -- an inline `background`
       would otherwise always beat it regardless. */
    background: var(--heat-bg, color-mix(in oklab, var(--ink) 16%, transparent));
    flex-shrink: 0;
    transition:
      background-color 150ms ease,
      transform 150ms ease;
  }
  .fleet-unit:hover,
  .fleet-unit:focus-visible {
    filter: brightness(1.3);
  }
  /* Stopped: muted, NOT enlarged -- a container turned off on purpose is
     common in a home-lab (see overviewStatus.ts's own doc), not a
     problem to flag the same way warning/serious/critical do. A plain
     darker-than-quiet ink tone, no hue, keeps it readably distinct from
     "running clean" without reading as an alarm. Full rework (its own
     shape/pattern, not just a color) is a later round. */
  .fleet-unit--stopped {
    background: color-mix(in oklab, var(--ink) 40%, transparent);
  }
  .fleet-unit--stopped:hover {
    background: color-mix(in oklab, var(--ink) 55%, transparent);
    filter: none;
  }
  /* Enlarged AND recolored -- two channels, not color alone, per the
     status-never-color-alone floor (the per-unit aria-label above is
     the third, for anyone who can't see either). Status colors always
     override heat -- these three never read --heat-bg at all. */
  .fleet-unit--warning,
  .fleet-unit--serious,
  .fleet-unit--critical {
    transform: scaleY(1.25);
    transform-origin: bottom;
  }
  .fleet-unit--warning {
    background: var(--status-warning);
  }
  .fleet-unit--warning:hover {
    background: var(--status-warning);
    filter: brightness(1.1);
  }
  .fleet-unit--serious {
    background: var(--status-serious);
  }
  .fleet-unit--serious:hover {
    background: var(--status-serious);
    filter: brightness(1.1);
  }
  .fleet-unit--critical {
    background: var(--status-critical);
  }
  .fleet-unit--critical:hover {
    background: var(--status-critical);
    filter: brightness(1.1);
  }

  /* Fixed-height label row, always present in layout (opacity-toggled,
     not conditionally rendered) so the strip's own position never
     shifts when a hover/focus starts or ends -- CoreBudgetRibbon's own
     hover-label convention. */
  .fleet-strip__label {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    min-height: 1.2rem;
    font-size: 0.8rem;
    color: var(--ink-2);
    opacity: 0;
    transition: opacity 150ms ease;
  }
  .fleet-strip__label--visible {
    opacity: 1;
  }
</style>
