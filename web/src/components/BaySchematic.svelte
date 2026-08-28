<!--
  BaySchematic: D2's miniature array visualization -- one usage-
  proportional bar per disk/pool entity. Deliberately dumb: it knows
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

  A flagged bar carries no floating callout text (corrective pass: a
  variable-length string like "95.0% capacity" centered over a 13px bar
  reliably collided with its neighbors). The outline alone marks it
  visually; the reason lives in the aria-label/title (for anyone
  hovering or using a screen reader) and, in full, in the "Needs a look"
  row right above this module -- the flagged unit's own color plus that
  row's text already carry the information, so nothing here needs to
  repeat it visibly.
-->
<script>
  import { fmtPct } from '../lib/format';
  import { seqStep } from '../lib/metrics';

  // entries: [{ slot, pct, flagged?: boolean, calloutText?: string,
  // solidState?: boolean }] -- pct is a plain 0-100 number (diskUsagePct's
  // own range); flagged/calloutText are the caller's own anomaly
  // decision, not derived here. calloutText (when given) folds into this
  // bar's own title/aria-label rather than rendering as visible text.
  // solidState (ask: "nvme storage vs spinning disk should stand out")
  // draws a distinct top-cap stroke, independent of the flagged outline
  // and the usage-proportional fill -- a type signal, not a health one.
  let { entries = [] } = $props();

  function labelFor(d) {
    const media = d.solidState ? ', solid state' : '';
    const base = `${d.slot}: ${fmtPct(d.pct)} used${media}`;
    return d.calloutText ? `${base} — ${d.calloutText}` : base;
  }
</script>

{#if entries.length > 0}
  <div class="bay-schematic">
    <span class="microlabel">Array &middot; {entries.length} member{entries.length === 1 ? '' : 's'}</span>
    <div class="bay-schematic__bars">
      {#each entries as d (d.slot)}
        <div
          class="bay-schematic__bar"
          class:bay-schematic__bar--flag={!!d.flagged}
          class:bay-schematic__bar--solid-state={!!d.solidState}
          role="img"
          title={labelFor(d)}
          aria-label={labelFor(d)}
        >
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
  /* Type signal, not a health one -- a plain ink-derived cap (never a
     --status-* token) so it reads as "different kind of member," not as
     another severity color, and stays legible alongside a flagged bar's
     own outline (border-box sizing keeps this inset from the outline). */
  .bay-schematic__bar--solid-state {
    border-top: 2px solid color-mix(in oklab, var(--ink) 50%, transparent);
  }
  .bay-schematic__fill {
    position: absolute;
    bottom: 0;
    left: 0;
    width: 100%;
    border-radius: 1px 1px 0 0;
  }
</style>
