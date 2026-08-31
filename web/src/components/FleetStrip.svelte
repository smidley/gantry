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

  Healthy running, stopped, and attention colors deliberately mirror
  the visible key exactly. Hover AND keyboard focus reveal a small label
  (icon+name+CPU+mem, CoreBudgetRibbon's own hover-label convention)
  since the strip otherwise carries this information in aria-label alone.

  Active-now pass (Scott: "Container fleet shows glowing blocks as
  active now, but they all look the same"): the box-shadow pulse alone
  couldn't separate an active unit from a quiet one because both shared
  the identical full-accent FILL. Quiet running units are now muted
  (--fleet-running, accent mixed well down toward the group surface)
  and an active unit gets the full accent fill PLUS a glow that never
  drops to zero mid-pulse -- distinct even in a static screenshot, and
  under reduced motion (static glow, no pulse). "Active" itself is
  unchanged and deliberately not near-zero: cpu.pct > 1 is one percent
  of the WHOLE HOST (host-share, not docker-stats per-core), the same
  bar the Containers view's "Active now" filter and each unit's
  aria-label already share. Status colors stay the loudest layer: a
  warning/serious/critical unit keeps its status fill even while
  active (the glow then pulses in that same status color), and stopped
  stays muted ink. Animation honors motion.reduced -- the Settings
  animation override folded together with the OS preference -- via the
  --still class, not a bare prefers-reduced-motion media query (which
  could never honor Settings' "on" override).
-->
<script>
  import { containerHealthStatus } from '../lib/containerStatus';
  import { fmtBytes, fmtPct } from '../lib/format';
  import { motion } from '../lib/motion.svelte';
  import ContainerIcon from './ContainerIcon.svelte';

  // containers: [{ name, state, health, icon?, cpuPct?, memBytes? }] --
  // cpuPct/memBytes are the live cpu.pct/mem.bytes samples (absent for a
  // container with no metrics yet), read straight off the live frame by
  // the caller.
  let { containers = [] } = $props();

  let hoveredName = $state(null);
  let hoveredContainer = $derived(containers.find((c) => c.name === hoveredName) ?? null);
  let runningContainers = $derived(containers.filter((c) => c.state === 'running'));
  let stoppedContainers = $derived(containers.filter((c) => c.state !== 'running'));
  let runningCount = $derived(runningContainers.length);
  let stoppedCount = $derived(stoppedContainers.length);
  let activeCount = $derived(
    runningContainers.filter((c) => Number.isFinite(c.cpuPct) && c.cpuPct > 1).length,
  );
  let groups = $derived.by(() => {
    const split = [
      { key: 'running', label: 'Running', items: runningContainers },
      { key: 'stopped', label: 'Stopped', items: stoppedContainers },
    ];
    return containers.length === 0 ? [split[0]] : split.filter((group) => group.items.length > 0);
  });
  let attentionCount = $derived(
    containers.filter((c) => c.state === 'running' && containerHealthStatus(c.state, c.health) !== 'good').length,
  );
  let attentionStatuses = $derived.by(() => {
    const present = new Set(
      runningContainers
        .map((c) => containerHealthStatus(c.state, c.health))
        .filter((status) => status !== 'good'),
    );
    return ['warning', 'serious', 'critical'].filter((status) => present.has(status));
  });

  function ariaLabel(name, state, health, cpuPct) {
    const meaningfulHealth = health === 'unhealthy' || health === 'starting';
    const stateLabel = meaningfulHealth ? `${name}: ${state}, ${health}` : `${name}: ${state}`;
    return state === 'running' && Number.isFinite(cpuPct) && cpuPct > 1
      ? `${stateLabel}, ${fmtPct(cpuPct)} CPU`
      : stateLabel;
  }
</script>

<section class="fleet-strip-wrap" class:fleet-strip-wrap--still={motion.reduced} aria-labelledby="fleet-strip-title">
  <div class="fleet-strip__head">
    <div>
      <h3 id="fleet-strip-title" class="fleet-strip__title">Container fleet</h3>
      <p class="fleet-strip__summary">
        <a href="#/containers?state=running"
          ><i class="fleet-strip__key fleet-strip__key--running" aria-hidden="true"></i>{runningCount} running</a
        >
        {#if activeCount > 0}
          <a href="#/containers?state=active"
            ><i class="fleet-strip__key fleet-strip__key--activity" aria-hidden="true"></i>{activeCount} active now</a
          >
        {/if}
        {#if stoppedCount > 0}
          <a href="#/containers?state=stopped"
            ><i class="fleet-strip__key fleet-strip__key--stopped" aria-hidden="true"></i>{stoppedCount} stopped</a
          >
        {/if}
        {#if attentionCount > 0}
          <a href="#/containers?state=attention"
            ><span class="fleet-strip__key-stack" aria-hidden="true"
              >{#each attentionStatuses as status}<i
                  class="fleet-strip__key"
                  style={`background:var(--status-${status})`}
                ></i>{/each}</span
            >{attentionCount} {attentionCount === 1 ? 'needs' : 'need'} attention</a
          >
        {/if}
      </p>
    </div>
    <a class="fleet-strip__link" href="#/containers">View all <span aria-hidden="true">&rarr;</span></a>
  </div>
  <div class="fleet-strip__groups" role="group" aria-label={`Container fleet, ${containers.length} total`}>
    {#each groups as group (group.key)}
      <div class="fleet-strip__group" class:fleet-strip__group--stopped={group.key === 'stopped'}>
        <a class="fleet-strip__group-head" href={`#/containers?state=${group.key}`}>
          <span class={`fleet-strip__group-dot fleet-strip__group-dot--${group.key}`} aria-hidden="true"></span>
          <span class="fleet-strip__group-name">{group.label}</span>
          <span class="fleet-strip__group-count tabular-nums">{group.items.length}</span>
        </a>
        <ul class="fleet-strip" aria-label={`${group.label} containers, ${group.items.length}`}>
          {#each group.items as c, i (c.name)}
            {@const stopped = group.key === 'stopped'}
            {@const status = containerHealthStatus(c.state, c.health)}
            {@const active = !stopped && Number.isFinite(c.cpuPct) && c.cpuPct > 1}
            <li>
              <a
                class="fleet-unit"
                class:fleet-unit--stopped={stopped}
                class:fleet-unit--active={active}
                class:fleet-unit--busy={active && c.cpuPct >= 10}
                class:fleet-unit--warning={!stopped && status === 'warning'}
                class:fleet-unit--serious={!stopped && status === 'serious'}
                class:fleet-unit--critical={!stopped && status === 'critical'}
                href={`#/containers/${encodeURIComponent(c.name)}`}
                aria-label={ariaLabel(c.name, c.state, c.health, c.cpuPct)}
                style={active ? `--activity-delay: -${i * 137}ms` : undefined}
                onmouseenter={() => (hoveredName = c.name)}
                onmouseleave={() => (hoveredName = null)}
                onfocus={() => (hoveredName = c.name)}
                onblur={() => (hoveredName = null)}
              ></a>
            </li>
          {/each}
        </ul>
      </div>
    {/each}
  </div>
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
    {:else}
      <span>Glowing blocks are active now. Select any block for details.</span>
    {/if}
  </div>
</section>

<style>
  .fleet-strip-wrap {
    /* --fleet-running: the quiet "running clean" fill -- accent mixed
       well down toward the group surface so the full-accent ACTIVE
       fill reads as the bright exception, not the wallpaper (the
       active-now pass, top-of-file doc). Defined once here so the
       units, the summary key, and the group dot can never drift apart. */
    --fleet-running: color-mix(in oklab, var(--accent) 45%, var(--surface-soft));
    display: flex;
    flex-direction: column;
    gap: 0.8rem;
    width: 100%;
    padding: 1rem;
    border: 1px solid var(--border);
    border-radius: 12px;
    background: color-mix(in oklab, var(--surface) 78%, transparent);
  }
  .fleet-strip__head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 1rem;
  }
  .fleet-strip__title {
    margin: 0;
    color: var(--ink);
    font-size: 0.92rem;
    font-weight: 650;
    letter-spacing: -0.015em;
  }
  .fleet-strip__summary {
    display: flex;
    flex-wrap: wrap;
    gap: 0.35rem 0.8rem;
    margin: 0.3rem 0 0;
    color: var(--ink-2);
    font-size: 0.76rem;
  }
  .fleet-strip__summary > a {
    display: inline-flex;
    align-items: center;
    gap: 0.3rem;
    color: inherit;
    text-decoration: none;
  }
  .fleet-strip__summary > a:hover {
    color: var(--accent-strong);
  }
  .fleet-strip__key {
    width: 7px;
    height: 7px;
    flex-shrink: 0;
    border-radius: 2px;
    background: var(--fleet-running);
  }
  .fleet-strip__key--stopped {
    background: color-mix(in oklab, var(--ink) 40%, transparent);
  }
  /* The activity key teaches the active encoding: full accent fill +
     the same never-zero glow the units themselves carry. */
  .fleet-strip__key--activity {
    --unit-color: var(--accent);
    background: var(--accent);
    animation: fleet-activity-glow 2.2s ease-in-out infinite;
  }
  .fleet-strip__key-stack {
    display: inline-flex;
    align-items: center;
    gap: 2px;
  }
  .fleet-strip__link {
    flex-shrink: 0;
    color: var(--accent);
    font-size: 0.76rem;
    font-weight: 600;
    text-decoration: none;
  }
  .fleet-strip__link:hover {
    color: var(--accent-strong);
  }
  .fleet-strip__groups {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .fleet-strip__group {
    display: grid;
    grid-template-columns: 5.5rem minmax(0, 1fr);
    align-items: center;
    gap: 0.75rem;
    min-height: 2.65rem;
    padding: 0.6rem 0.7rem;
    border: 1px solid color-mix(in oklab, var(--border) 78%, transparent);
    border-radius: 9px;
    background: var(--surface-soft);
  }
  .fleet-strip__group--stopped {
    background: color-mix(in oklab, var(--ink) 2.5%, var(--surface-soft));
  }
  .fleet-strip__group-head {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    min-width: 0;
    color: inherit;
    text-decoration: none;
  }
  .fleet-strip__group-head:hover .fleet-strip__group-name {
    color: var(--accent-strong);
  }
  .fleet-strip__group-dot {
    width: 7px;
    height: 7px;
    flex-shrink: 0;
    border-radius: 2px;
    background: var(--fleet-running);
  }
  .fleet-strip__group-dot--stopped {
    background: color-mix(in oklab, var(--ink) 40%, transparent);
  }
  .fleet-strip__group-name {
    overflow: hidden;
    color: var(--ink);
    font-size: 0.74rem;
    font-weight: 600;
    text-overflow: ellipsis;
  }
  .fleet-strip__group-count {
    margin-left: auto;
    color: var(--ink-2);
    font-size: 0.7rem;
  }
  /* A plain <ul>/<li> pair -- not a <div role="list">/<a role="listitem">
     one -- carries list/listitem semantics implicitly and correctly: an
     explicit role="listitem" on the <a> itself is invalid (an
     interactive element can't take a non-interactive ARIA role) and
     Svelte's own a11y check rejects it. */
  /* Region-sizing pass (Scott: the strip wrapped as ragged rows of
     blocks): a fixed-pitch GRID, not flex-wrap. auto-fill lays every
     row on the same explicit column tracks, so a wrapped strip reads
     as one deliberate contribution-graph grid -- columns align
     vertically, each row holds only whole units (never a clipped
     partial at the edge), and the sub-pitch remainder stays as quiet
     trailing space. The wider row-gap (vs the 2px column-gap) is what
     makes a second row read as a ROW rather than more noise, and
     absorbs the flagged units' scaleY(1.25) overshoot (transforms
     don't take layout space -- under flex they overlapped the line
     above). */
  .fleet-strip {
    display: grid;
    grid-template-columns: repeat(auto-fill, 8px);
    justify-content: start;
    gap: 4px 2px;
    max-width: 100%;
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .fleet-strip li {
    display: flex;
  }
  .fleet-unit {
    --unit-color: var(--fleet-running);
    display: block;
    width: 8px;
    height: 16px;
    border-radius: 1px;
    background: var(--unit-color);
    flex-shrink: 0;
    transition:
      background-color 150ms ease,
      transform 150ms ease;
  }
  .fleet-unit:hover,
  .fleet-unit:focus-visible {
    filter: brightness(1.3);
  }
  /* Active now (cpu.pct > 1, host-share -- see the top-of-file doc):
     the bright exception against the muted running fill, distinct with
     animation off; the never-zero glow adds the live pulse on top.
     Declared BEFORE the status classes below so a unit that is both
     active and warning/serious/critical keeps the STATUS fill -- status
     stays the loudest layer -- while its glow then pulses in that same
     status color via --unit-color. */
  .fleet-unit--active {
    --unit-color: var(--accent);
    background: var(--unit-color);
    animation: fleet-activity-glow 2.2s ease-in-out infinite;
    animation-delay: var(--activity-delay, 0ms);
  }
  .fleet-unit--busy {
    animation-name: fleet-activity-glow-busy;
    animation-duration: 1.45s;
  }
  /* Stopped: muted, NOT enlarged -- a container turned off on purpose is
     common in a home-lab (see overviewStatus.ts's own doc), not a
     problem to flag the same way warning/serious/critical do. A plain
     darker-than-quiet ink tone, no hue, keeps it readably distinct from
     "running clean" without reading as an alarm. Full rework (its own
     shape/pattern, not just a color) is a later round. */
  .fleet-unit--stopped {
    --unit-color: color-mix(in oklab, var(--ink) 40%, transparent);
    background: var(--unit-color);
  }
  .fleet-unit--stopped:hover {
    background: color-mix(in oklab, var(--ink) 55%, transparent);
    filter: none;
  }
  /* Enlarged AND recolored -- two channels, not color alone, per the
     status-never-color-alone floor (the per-unit aria-label above is
     the third, for anyone who can't see either). */
  .fleet-unit--warning,
  .fleet-unit--serious,
  .fleet-unit--critical {
    transform: scaleY(1.25);
    transform-origin: bottom;
  }
  .fleet-unit--warning {
    --unit-color: var(--status-warning);
    background: var(--unit-color);
  }
  .fleet-unit--warning:hover {
    background: var(--status-warning);
    filter: brightness(1.1);
  }
  .fleet-unit--serious {
    --unit-color: var(--status-serious);
    background: var(--unit-color);
  }
  .fleet-unit--serious:hover {
    background: var(--status-serious);
    filter: brightness(1.1);
  }
  .fleet-unit--critical {
    --unit-color: var(--status-critical);
    background: var(--unit-color);
  }
  .fleet-unit--critical:hover {
    background: var(--status-critical);
    filter: brightness(1.1);
  }
  /* Both glows bottom out at a visible base rather than zero -- an
     active unit stays lit between pulse peaks, so activity survives a
     glance (and a screenshot), not just a patient stare. */
  @keyframes fleet-activity-glow {
    0%,
    100% {
      box-shadow: 0 0 3px 0.5px color-mix(in oklab, var(--unit-color) 32%, transparent);
    }
    50% {
      box-shadow: 0 0 8px 2px color-mix(in oklab, var(--unit-color) 60%, transparent);
    }
  }
  @keyframes fleet-activity-glow-busy {
    0%,
    100% {
      box-shadow: 0 0 4px 1px color-mix(in oklab, var(--unit-color) 40%, transparent);
    }
    50% {
      box-shadow: 0 0 12px 3.5px color-mix(in oklab, var(--unit-color) 72%, transparent);
    }
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
    opacity: 0.72;
    transition:
      color 150ms ease,
      opacity 150ms ease;
  }
  .fleet-strip__label--visible {
    opacity: 1;
    color: var(--ink);
  }

  @media (max-width: 27rem) {
    .fleet-strip__group {
      grid-template-columns: 1fr;
      gap: 0.5rem;
    }
    .fleet-strip__group-count {
      margin-left: 0;
    }
  }

  /* motion.reduced (the Settings animation override resolved together
     with the OS preference -- lib/motion.svelte.ts) rather than a bare
     prefers-reduced-motion media query: the media query alone could
     never honor Settings' "on" (force animations even when the OS says
     reduce). The bright active fill needs no substitute; the pulse
     collapses to its constant mid-strength glow. */
  .fleet-strip-wrap--still .fleet-strip__key--activity,
  .fleet-strip-wrap--still .fleet-unit--active,
  .fleet-strip-wrap--still .fleet-unit--busy {
    animation: none;
    box-shadow: 0 0 5px 1px color-mix(in oklab, var(--unit-color) 45%, transparent);
  }
</style>
