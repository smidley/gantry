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
  full information survives with color entirely removed.

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
  under reduced motion (static glow, no pulse). Status colors stay the
  loudest layer: a warning/serious/critical unit keeps its status fill
  even while active (the glow then pulses in that same status color),
  and stopped stays muted ink. Animation honors motion.reduced -- the
  Settings animation override folded together with the OS preference --
  via the --still class, not a bare prefers-reduced-motion media query
  (which could never honor Settings' "on" override).

  Any-metric glow pass (Scott: "Glowing Container activity should be
  triggered by any metric that is above a threshold, not just CPU"):
  "active" is no longer cpu.pct alone. lib/fleetActivity.ts ranks a
  container's cpu, memory, network, disk IO and GPU readings on one
  shared 0..1 elevation scale and the MAX one drives the glow, so a
  container saturating a disk while idling its CPU finally lights up.
  The two glow tiers are unchanged and land in exactly the same place
  they did (see BUSY_ELEVATION's own doc), and the driving metric is
  NAMED -- in the hover label and in the unit's own aria-label -- so a
  glowing block explains itself instead of just looking busy.

  Auto-sizing pass (Scott: "Container fleet section should be sized to
  take up the available screen space beneath it, and objects
  (containers) inside the section should be auto-resized depending on
  quantity of containers. For example, if there's only 3 containers,
  they will be larger, and if there are 30 containers, the blocks will
  be smaller"). Two halves, both driven off ONE measurement of the
  block field:

  1. THE FIELD takes the screen space left beneath it -- viewport height
     minus the field's own distance down the document, minus a gutter so
     the next section still peeks and invites a scroll -- but never more
     than the fit actually uses. "Don't waste room" is the ask; holding
     a void open under three blocks would be the same waste in the other
     direction.

  2. THE BLOCKS are square and sized by fitFleetCells -- the largest
     square that still fits them all in the measured field. Three blocks
     come out big enough to carry their own name label; forty come out
     small. Only when even the floor can't fit does the field scroll.

  Tuning pass (Scott, on the first cut: "I liked the smaller sized
  container blocks better. Let's not allow them to get quite so big." +
  "Keep the stopped containers in a separate section like we had
  before."). Two changes, and the second is the one with teeth:

  - CELL_MAX came down to 64, chosen so the name tier (56px) is still
    reachable AT the ceiling -- a block that can never carry its name
    would have made that whole tier decorative.

  - The running/stopped split is two real GRIDS again, running first and
    a labelled stopped sub-section under it, rather than one grid with
    the stopped units muted at its tail. They share ONE cell size and
    ONE column count -- two independently-packed pitches would read as
    two unrelated things rather than one fleet -- which is why the group
    shape is an INPUT to the fit (fitFleetCells takes a count per group,
    not a total) rather than something applied to its answer. Splitting
    genuinely costs height: a running group that doesn't fill its last
    row still ends it, and the stopped label between them is real space.
    Both are budgeted before the size is chosen, so the rendered stack
    matches what the fit promised instead of quietly overflowing it. An
    empty stopped set renders no sub-section and costs no gap.
-->
<script>
  import { containerHealthStatus } from '../lib/containerStatus';
  import { fmtBytes, fmtPct } from '../lib/format';
  import { motion } from '../lib/motion.svelte';
  import { activityInputFor, fleetActivity } from '../lib/fleetActivity';
  import { fitFleetCells, fleetGridHeight } from '../lib/fleetGrid';
  import ContainerIcon from './ContainerIcon.svelte';

  // containers: [{ name, state, health, icon?, metrics? }] -- metrics is
  // the container's own live bag straight off the frame, handed through
  // whole rather than pre-picked so the glow rule can read anything in
  // it without this component's prop shape gaining a field per metric.
  // hostMemBytes is the host's total memory (the frame carries it only
  // implicitly -- see activityInputFor); absent just means a container
  // with no memory LIMIT contributes no memory reading.
  let { containers = [], hostMemBytes = undefined } = $props();

  // CELL_GAP/CELL_MIN/CELL_MAX: the block grid's own clamps.
  // CELL_MIN keeps a unit individually tappable at the small end (the
  // 8px-wide floor the fixed-pitch strip already held, squared up);
  // CELL_MAX is where a block stops reading as a block and starts
  // reading as a card. Between them the size is entirely computed.
  //
  // The ceiling came DOWN from 120 (Scott, on the first cut: "I liked
  // the smaller sized container blocks better. Let's not allow them to
  // get quite so big."). 64 is the size at which a block still carries
  // its own name -- NAME_CELL_PX is 56, so the label tier stays
  // reachable at the ceiling rather than becoming decorative -- while a
  // 46-container fleet reads as a dense wall rather than a page of
  // cards. It also means the ceiling binds more often: below roughly a
  // dozen blocks in a full-width field everything simply sits at 64,
  // and the count only starts driving the size once the area runs out.
  const CELL_GAP = 6;
  const CELL_MIN = 12;
  const CELL_MAX = 64;
  // NAME_CELL_PX: what a block earns as it grows. Below it the strip is
  // pure density -- the contribution-graph reading it has always had --
  // and a name would be unreadable noise. There is deliberately no icon
  // tier any more: it sat at 96px, which the ceiling above can no longer
  // reach, and an icon crammed in beside a name at 64px reads as clutter
  // rather than information. The hover label already shows the icon.
  const NAME_CELL_PX = 56;

  // GROUP_GAP_PX: the vertical space one group boundary costs -- the
  // stopped sub-section's own label plus its breathing room. It is an
  // INPUT to the fit (see lib/fleetGrid.ts): the two groups share one
  // cell size, so the space between them has to be budgeted before the
  // size is chosen, not subtracted after.
  const GROUP_GAP_PX = 24;

  // FIELD_PEEK_PX: what stays visible below the whole fleet SECTION, so
  // a full-height fleet shows the top of whatever follows rather than
  // pretending the page ends there. Measured against the section, not
  // the grid -- the label row and the card's own padding sit between
  // the two, and a reserve that forgot them would quietly spend them.
  // GRID_MIN_PX is the floor for a fleet sitting low on the page (or an
  // empty one); GRID_MAX_PX keeps a very tall display from handing the
  // fleet a whole screen it has no use for. Both are the GRID's own
  // height -- the field's padding and border sit outside it.
  const FIELD_PEEK_PX = 72;
  const GRID_MIN_PX = 96;
  const GRID_MAX_PX = 560;

  let hoveredName = $state(null);

  // units carries everything derived per container, once: the run split,
  // the status vocabulary, and the activity reading the glow, the
  // summary count, the aria-label and the hover label all share.
  let units = $derived(
    containers.map((c) => {
      const stopped = c.state !== 'running';
      return {
        name: c.name,
        state: c.state,
        health: c.health,
        icon: c.icon,
        metrics: c.metrics ?? {},
        stopped,
        status: containerHealthStatus(c.state, c.health),
        // A stopped container reports no activity by definition -- its
        // last samples may still be in the frame, and glowing over them
        // would claim it is doing work it cannot be doing.
        activity: stopped
          ? { active: false, busy: false, metric: null, value: 0, elevation: 0, label: null }
          : fleetActivity(activityInputFor(c.metrics, hostMemBytes)),
      };
    }),
  );

  // Two real groups again (Scott: "Keep the stopped containers in a
  // separate section like we had before") -- running, then a labelled
  // stopped sub-section. They are separate GRIDS but share one cell size
  // and one column count, because two independently-packed pitches would
  // read as two unrelated things rather than one fleet; fitFleetCells
  // solves both at once for exactly that reason. An empty stopped set
  // renders no sub-section at all, not an empty one.
  let running = $derived(units.filter((u) => !u.stopped));
  let stopped = $derived(units.filter((u) => u.stopped));
  let groups = $derived(
    [
      { key: 'running', label: null, items: running },
      { key: 'stopped', label: 'stopped', items: stopped },
    ].filter((g) => g.items.length > 0),
  );
  let hoveredUnit = $derived(units.find((u) => u.name === hoveredName) ?? null);

  let runningCount = $derived(running.length);
  let stoppedCount = $derived(stopped.length);
  let activeCount = $derived(units.filter((u) => u.activity.active).length);
  let attentionCount = $derived(units.filter((u) => !u.stopped && u.status !== 'good').length);
  let attentionStatuses = $derived.by(() => {
    const present = new Set(units.filter((u) => !u.stopped && u.status !== 'good').map((u) => u.status));
    return ['warning', 'serious', 'critical'].filter((status) => present.has(status));
  });

  // --- Field measurement --------------------------------------------------
  //
  // The GRID is what gets measured, not the padded field around it:
  // clientWidth is the space cells actually have, with the field's own
  // padding and border already out of it and (when the grid is
  // scrolling) the scrollbar too. Measuring the field's border box
  // instead would hand the fit a width the cells don't have and buy one
  // column too many.
  //
  // There is no loop to close even though the field's height is set
  // from this measurement: only the grid's WIDTH and its distance down
  // the document are read, and neither depends on how tall the field is.
  // A narrower reading (a scrollbar arriving) only ever yields fewer
  // columns and so more rows, never fewer -- it cannot oscillate.
  // Distance down the DOCUMENT is scroll-independent (rect.top +
  // scrollY), so scrolling the page never resizes the fleet either.
  let wrapEl = $state();
  let stripEl = $state();
  let fieldWidth = $state(0);
  let spaceBeneath = $state(0);

  function measure() {
    if (!stripEl || !wrapEl) return;
    fieldWidth = stripEl.clientWidth;
    const gridRect = stripEl.getBoundingClientRect();
    const docTop = gridRect.top + window.scrollY;
    // Everything of this section that sits BELOW the grid -- the hover
    // label and the card's own bottom padding. Constant, and measured
    // rather than guessed at, so the peek reserve below is a reserve
    // under the SECTION and not just under the blocks.
    const belowGrid = Math.max(0, wrapEl.getBoundingClientRect().bottom - gridRect.bottom);
    spaceBeneath = Math.max(0, window.innerHeight - docTop - belowGrid - FIELD_PEEK_PX);
  }

  $effect(() => {
    if (!stripEl || !wrapEl || typeof ResizeObserver === 'undefined') return;
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(stripEl);
    // A viewport-height change alone doesn't resize the grid (its width
    // is unchanged and its height is ours), so the window needs its own
    // listener for the space-beneath half of the measurement.
    window.addEventListener('resize', measure);
    return () => {
      ro.disconnect();
      window.removeEventListener('resize', measure);
    };
  });

  // Two passes, in this order and not the other way round. First OFFER
  // the blocks everything that's going spare and let fitFleetCells size
  // them against it -- that offer is what makes the size depend on the
  // count at all. Then size the grid to what the fit actually USED, so
  // a fleet small enough to hit the ceiling stops at the row it needs
  // instead of holding the rest of the screen open under it.
  //
  // The height lands on the GRID, not on the padded field around it:
  // the fit's own answer is a number of block rows, and adding the
  // field's padding and border back onto it before setting it would be
  // one more place to get that arithmetic wrong (and did -- the first
  // cut clipped a row).
  let offeredHeight = $derived(Math.max(GRID_MIN_PX, Math.min(GRID_MAX_PX, spaceBeneath)));
  let fit = $derived(
    fitFleetCells({
      counts: groups.map((g) => g.items.length),
      width: fieldWidth,
      height: offeredHeight,
      gap: CELL_GAP,
      groupGap: GROUP_GAP_PX,
      min: CELL_MIN,
      max: CELL_MAX,
    }),
  );
  let fieldHeight = $derived(
    Math.max(GRID_MIN_PX, Math.min(offeredHeight, fleetGridHeight(fit, CELL_GAP, GROUP_GAP_PX))),
  );
  let showNames = $derived(fit.cell >= NAME_CELL_PX);

  function ariaLabel(unit) {
    const meaningfulHealth = unit.health === 'unhealthy' || unit.health === 'starting';
    const stateLabel = meaningfulHealth ? `${unit.name}: ${unit.state}, ${unit.health}` : `${unit.name}: ${unit.state}`;
    return unit.activity.label ? `${stateLabel}, ${unit.activity.label}` : stateLabel;
  }
</script>

<section
  class="fleet-strip-wrap"
  class:fleet-strip-wrap--still={motion.reduced}
  aria-labelledby="fleet-strip-title"
  bind:this={wrapEl}
>
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
  <!-- The STACK carries the computed height and is the measured element
    (see the auto-sizing pass); the field just wraps it. Both grids sit
    inside it at one shared pitch, separated by the stopped section's own
    label, whose height IS the group gap the fit budgeted -- so the CSS
    and the arithmetic can't drift apart. It only ever scrolls when even
    the min cell couldn't fit, which fitFleetCells reports rather than
    the CSS guessing at it. -->
  <div class="fleet-strip__field">
    <div
      class="fleet-strip__stack"
      class:fleet-strip__stack--scroll={fit.overflow}
      style={`--cell:${fit.cell}px; --cell-gap:${CELL_GAP}px; --cols:${Math.max(1, fit.cols)}; --group-gap:${GROUP_GAP_PX}px; height:${fieldHeight}px`}
      role="group"
      aria-label={`Container fleet, ${units.length} total`}
      data-cell={fit.cell}
      bind:this={stripEl}
    >
      {#each groups as group (group.key)}
        {#if group.label}
          <a class="fleet-strip__group-label" href="#/containers?state=stopped">
            <span class="microlabel">{group.items.length} {group.label}</span>
          </a>
        {/if}
        <ul class="fleet-strip" aria-label={`${group.key === 'stopped' ? 'Stopped' : 'Running'} containers, ${group.items.length}`}>
          {#each group.items as u, i (u.name)}
            <li>
              <a
                class="fleet-unit"
                class:fleet-unit--stopped={u.stopped}
                class:fleet-unit--active={u.activity.active}
                class:fleet-unit--busy={u.activity.busy}
                class:fleet-unit--warning={!u.stopped && u.status === 'warning'}
                class:fleet-unit--serious={!u.stopped && u.status === 'serious'}
                class:fleet-unit--critical={!u.stopped && u.status === 'critical'}
                class:fleet-unit--named={showNames}
                href={`#/containers/${encodeURIComponent(u.name)}`}
                aria-label={ariaLabel(u)}
                data-metric={u.activity.metric ?? undefined}
                style={u.activity.active ? `--activity-delay: -${i * 137}ms` : undefined}
                onmouseenter={() => (hoveredName = u.name)}
                onmouseleave={() => (hoveredName = null)}
                onfocus={() => (hoveredName = u.name)}
                onblur={() => (hoveredName = null)}
              >
                {#if showNames}<span class="fleet-unit__name" aria-hidden="true">{u.name}</span>{/if}
              </a>
            </li>
          {/each}
        </ul>
      {/each}
    </div>
  </div>
  <div class="fleet-strip__label" class:fleet-strip__label--visible={!!hoveredUnit}>
    {#if hoveredUnit}
      <ContainerIcon name={hoveredUnit.name} icon={hoveredUnit.icon} size={14} />
      <span>{hoveredUnit.name}</span>
      {#if hoveredUnit.metrics['cpu.pct'] !== undefined}
        <span class="tabular-nums">{fmtPct(hoveredUnit.metrics['cpu.pct'])}</span>
      {/if}
      {#if hoveredUnit.metrics['mem.bytes'] !== undefined}
        <span class="tabular-nums">{fmtBytes(hoveredUnit.metrics['mem.bytes'])}</span>
      {/if}
      {#if hoveredUnit.activity.label}
        <span class="fleet-strip__label-glow">glowing: {hoveredUnit.activity.label}</span>
      {/if}
    {:else}
      <span>A block glows when its CPU, memory, network, disk or GPU is busy. Select any block for details.</span>
    {/if}
  </div>
</section>

<style>
  .fleet-strip-wrap {
    /* --fleet-running: the quiet "running clean" fill -- accent mixed
       well down toward the field surface so the full-accent ACTIVE
       fill reads as the bright exception, not the wallpaper (the
       active-now pass, top-of-file doc). Defined once here so the
       units and the summary key can never drift apart. */
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
  /* The field is only the panel around the stack -- its height comes
     from the stack's own inline height, which is the fit's answer in
     block rows. Nothing here derives a WIDTH from content, which is
     what keeps the ResizeObserver from feeding itself. */
  .fleet-strip__field {
    min-width: 0;
    padding: 0.6rem 0.7rem;
    border: 1px solid color-mix(in oklab, var(--border) 78%, transparent);
    border-radius: 9px;
    background: var(--surface-soft);
  }
  /* The stack holds both groups at one pitch. No `gap` of its own: the
     stopped label's own height IS the group gap the fit budgeted
     (--group-gap), so the rendered height matches fleetGridHeight
     exactly rather than approximately. */
  .fleet-strip__stack {
    display: flex;
    flex-direction: column;
    min-width: 0;
    overflow: hidden;
  }
  /* Only when even the min cell couldn't fit -- fitFleetCells says so,
     the CSS doesn't guess. Anywhere above that floor the blocks shrink
     instead, so a fleet never scrolls while it still had size to give. */
  .fleet-strip__stack--scroll {
    overflow-y: auto;
  }
  /* The stopped section's own heading, sized to the exact gap the fit
     reserved for it. It doubles as the link into the filtered Containers
     view, the same destination the summary line's "N stopped" carries --
     one vocabulary, two places to reach it from. */
  .fleet-strip__group-label {
    display: flex;
    align-items: flex-end;
    height: var(--group-gap, 24px);
    padding-bottom: 0.3rem;
    box-sizing: border-box;
    color: inherit;
    text-decoration: none;
  }
  .fleet-strip__group-label:hover .microlabel {
    color: var(--accent-strong);
  }
  /* A plain <ul>/<li> pair -- not a <div role="list">/<a role="listitem">
     one -- carries list/listitem semantics implicitly and correctly: an
     explicit role="listitem" on the <a> itself is invalid (an
     interactive element can't take a non-interactive ARIA role) and
     Svelte's own a11y check rejects it. */
  /* An explicit column count at an explicit cell size, both computed
     (lib/fleetGrid.ts) rather than declared: every row breaks on the
     same whole-unit boundaries, columns align vertically by
     construction, and no unit is ever clipped at the edge.
     align-content: start keeps a part-full last row against the rest
     instead of stretching the gaps to fill. */
  .fleet-strip {
    display: grid;
    grid-template-columns: repeat(var(--cols, 1), var(--cell, 10px));
    grid-auto-rows: var(--cell, 10px);
    align-content: start;
    justify-content: start;
    gap: var(--cell-gap, 4px);
    width: 100%;
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .fleet-strip li {
    display: flex;
    min-width: 0;
  }
  .fleet-unit {
    --unit-color: var(--fleet-running);
    display: block;
    width: 100%;
    height: 100%;
    border-radius: 2px;
    background: var(--unit-color);
    overflow: hidden;
    transition:
      background-color 150ms ease,
      transform 150ms ease;
  }
  .fleet-unit:hover,
  .fleet-unit:focus-visible {
    filter: brightness(1.3);
  }
  /* A block only earns a name once it's genuinely big (NAME_CELL_PX) --
     below that the strip is pure density and a name would be
     unreadable noise. */
  .fleet-unit--named {
    display: flex;
    align-items: flex-end;
    padding: 0.28rem;
    border-radius: 4px;
  }
  .fleet-unit__name {
    width: 100%;
    overflow: hidden;
    color: color-mix(in oklab, var(--ink) 78%, var(--unit-color));
    font-family: var(--font-mono);
    font-size: 0.62rem;
    line-height: 1.2;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  /* Active now (the max elevation across cpu/mem/net/io/gpu -- see the
     any-metric glow pass in the top-of-file doc): the bright exception
     against the muted running fill, distinct with animation off; the
     never-zero glow adds the live pulse on top. Declared BEFORE the
     status classes below so a unit that is both active and warning/
     serious/critical keeps the STATUS fill -- status stays the loudest
     layer -- while its glow then pulses in that same status color via
     --unit-color. */
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
  /* Recolored AND ringed -- two channels, not color alone, per the
     status-never-color-alone floor (the per-unit aria-label above is
     the third, for anyone who can't see either). A ring rather than the
     fixed-pitch strip's old scaleY stretch: a square cell has no spare
     row-gap to overshoot into, and an inset ring reads at every size
     the grid can now produce. */
  .fleet-unit--warning,
  .fleet-unit--serious,
  .fleet-unit--critical {
    box-shadow: inset 0 0 0 max(1px, calc(var(--cell, 10px) * 0.09)) color-mix(in oklab, var(--ink) 42%, transparent);
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
    flex-wrap: wrap;
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
  /* The one part of the hover label that says why a block is lit -- in
     the accent, because that is the color the glow itself is drawn in. */
  .fleet-strip__label-glow {
    color: var(--accent-strong);
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
