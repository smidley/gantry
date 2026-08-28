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
-->
<script>
  import { containerHealthStatus } from '../lib/containerStatus';

  let { containers = [] } = $props();

  function ariaLabel(name, state, health) {
    const meaningfulHealth = health === 'unhealthy' || health === 'starting';
    return meaningfulHealth ? `${name}: ${state}, ${health}` : `${name}: ${state}`;
  }
</script>

<ul class="fleet-strip" aria-label={`Container fleet, ${containers.length} total`}>
  {#each containers as c (c.name)}
    {@const status = containerHealthStatus(c.state, c.health)}
    <li>
      <a
        class="fleet-unit"
        class:fleet-unit--warning={status === 'warning'}
        class:fleet-unit--serious={status === 'serious'}
        class:fleet-unit--critical={status === 'critical'}
        href={`#/containers/${encodeURIComponent(c.name)}`}
        aria-label={ariaLabel(c.name, c.state, c.health)}
      ></a>
    </li>
  {/each}
</ul>

<style>
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
    background: color-mix(in oklab, var(--ink) 16%, transparent);
    flex-shrink: 0;
    transition:
      background-color 150ms ease,
      transform 150ms ease;
  }
  .fleet-unit:hover {
    background: color-mix(in oklab, var(--ink) 32%, transparent);
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
</style>
