<!--
  BaySchematic: D2's miniature array visualization -- one usage-
  proportional bar per disk/pool entity, quiet and wordless except for a
  callout on whichever bar(s) a caller flags. Deliberately dumb: it knows
  nothing about WHY an entry is flagged (that's Overview's own anomaly
  logic), only how to draw what it's handed -- the same "reads straight
  off the array/disk data" reusability the design's own recommendation
  calls for when this eventually promotes to a full Storage module.

  Fills use the existing --seq-100..--seq-700 ramp (seqStep, shared with
  Storage's own disk-usage bars and the old ArrayCard's pool rows) --
  zero new colors. Each bar is non-interactive (a `title` + aria-label
  pairing stands in for per-bar text, since real disk/pool names vary too
  much in length to reliably abbreviate into the mockup's fixed 2-letter
  labels without risking a misleading collision) rather than an always-
  visible label row.
-->
<script>
  import { fmtPct } from '../lib/format';
  import { seqStep } from '../lib/metrics';

  // entries: [{ slot, pct, flagged?: boolean, calloutText?: string }] --
  // pct is a plain 0-100 number (diskUsagePct's own range); flagged/
  // calloutText are the caller's own anomaly decision, not derived here.
  let { entries = [] } = $props();
</script>

{#if entries.length > 0}
  <div class="bay-schematic">
    <span class="microlabel">Array &middot; {entries.length} member{entries.length === 1 ? '' : 's'}</span>
    <div class="bay-schematic__bars">
      {#each entries as d (d.slot)}
        <div
          class="bay-schematic__bar"
          class:bay-schematic__bar--flag={!!d.flagged}
          role="img"
          title={`${d.slot}: ${fmtPct(d.pct)} used`}
          aria-label={`${d.slot}: ${fmtPct(d.pct)} used`}
        >
          {#if d.calloutText}<span class="bay-schematic__callout tabular-nums">{d.calloutText}</span>{/if}
          <div
            class="bay-schematic__fill"
            style={`height: ${Math.min(100, Math.max(0, d.pct))}%; background: ${seqStep(d.pct)}`}
          ></div>
        </div>
      {/each}
    </div>
  </div>
{/if}

<style>
  .bay-schematic {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .bay-schematic__bars {
    display: flex;
    align-items: flex-end;
    gap: 4px;
    height: 46px;
    flex-wrap: wrap;
  }
  .bay-schematic__bar {
    position: relative;
    width: 13px;
    height: 100%;
    background: color-mix(in oklab, var(--ink) 7%, transparent);
    border-radius: 1px;
    flex-shrink: 0;
  }
  .bay-schematic__bar--flag {
    outline: 1.5px solid var(--status-warning);
    outline-offset: 1px;
  }
  .bay-schematic__fill {
    position: absolute;
    bottom: 0;
    left: 0;
    width: 100%;
    border-radius: 1px 1px 0 0;
  }
  .bay-schematic__callout {
    position: absolute;
    bottom: 100%;
    left: 50%;
    transform: translateX(-50%);
    margin-bottom: 3px;
    font-family: var(--font-mono);
    font-size: 9.5px;
    color: var(--status-warning);
    white-space: nowrap;
  }
</style>
