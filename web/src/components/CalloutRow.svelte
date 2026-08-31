<!--
  CalloutRow: one "Needs a look" attention row, standalone -- Scott's
  three asks for the section in one component: the row is CLICKABLE
  (title links to the route that explains the concern, describeAnomaly's
  own href from anomalyHref -- one shared routing table), the reason
  stays INLINE on the same sentence (the header-compaction pass's
  one-line-per-row rule), and every row grows an ACK control ("we need
  to be able to acknowledge things... so they stop showing up for a
  period of time") offering 1h/24h/7d.

  The ack control routes by callout kind -- one mechanism per system:
  an alert-backed callout's ack IS an alert silence (createSilence on
  its own rule_id+entity; the frame's silenced flag then drops the row
  within a tick, since a silenced firing alert already contributes
  nothing to the derivation), while every frame-derived kind posts a
  concrete (kind, entity) ack (acks.svelte.ts; the derivation's own ack
  filter drops the row on the next tick). Neither path hides anything
  locally -- the row disappears only when the derivation's own inputs
  say so, so a failed POST can never quietly eat a real warning.

  Severity is two-channel here as everywhere: the dot's color AND the
  row's aria-label carry it (the HealthDot rule -- color alone never
  carries meaning), with the title text itself naming the concern.
-->
<script>
  import { describeAnomaly, ackKeyFor } from '../lib/overviewStatus';
  import { acks } from '../lib/acks.svelte';
  import { createSilence } from '../lib/api';

  // anomaly: one OverviewAnomaly (deriveOverviewStatus's own row).
  let { anomaly } = $props();

  let text = $derived(describeAnomaly(anomaly));

  // ackable: whether a quiet-this path exists for this row at all -- a
  // silence for an alert-backed callout, a (kind, entity) ack for the
  // frame-derived kinds. An insight-backed callout has neither
  // (ackKeyFor returns null for it): quieting a finding is DISMISSING
  // it on the Insights view this row already links to -- one mechanism
  // per system -- so the ack control doesn't render at all.
  let ackable = $derived(anomaly.kind === 'alert' || ackKeyFor(anomaly) !== null);

  // Ack menu state: closed -> the single "Ack" affordance; open -> the
  // three duration presets. pending disables the controls during the
  // POST; a failure surfaces the server's own message inline and leaves
  // the row exactly as it was (see module doc -- nothing hides locally).
  let open = $state(false);
  let pending = $state(false);
  let error = $state(null);

  // The 1h/24h/7d presets -- the same hour values the server bounds
  // POST /api/acks to (1..168), and well inside silences' own 1..720.
  const DURATIONS = [
    { label: '1h', hours: 1 },
    { label: '24h', hours: 24 },
    { label: '7d', hours: 168 },
  ];

  async function ackFor(hours) {
    pending = true;
    error = null;
    try {
      if (anomaly.kind === 'alert') {
        // An alert-backed callout: the ack IS a silence on the firing
        // rule/entity pair. Never both fields blank here -- ruleId is
        // always a real rule id -- so the global-scope gesture
        // (scope:"all") can never be needed on this path.
        await createSilence({ rule_id: anomaly.ruleId, entity: anomaly.entity, hours });
      } else {
        const key = ackKeyFor(anomaly);
        await acks.ack(key.kind, key.entity, hours);
      }
      open = false;
    } catch (err) {
      error = err?.message ?? 'Acknowledge failed.';
    } finally {
      pending = false;
    }
  }
</script>

<p class="callout-row">
  <span class="callout-row__dot" style={`background:var(--status-${text.severity})`} aria-hidden="true"></span>
  {#if text.href}
    <a class="callout-row__title" href={text.href}>{text.title}</a>
  {:else}
    <span class="callout-row__title">{text.title}</span>
  {/if}
  {#if text.detail}<span class="callout-row__detail">&mdash; {text.detail}</span>{/if}
  {#if ackable}
  <span class="callout-row__ack">
    {#if open}
      {#each DURATIONS as d (d.hours)}
        <button
          type="button"
          class="callout-row__ack-btn"
          disabled={pending}
          aria-label={`Acknowledge for ${d.label}: ${text.title}`}
          onclick={() => ackFor(d.hours)}
        >
          {d.label}
        </button>
      {/each}
      <button
        type="button"
        class="callout-row__ack-btn callout-row__ack-cancel"
        disabled={pending}
        aria-label={`Cancel acknowledging: ${text.title}`}
        onclick={() => (open = false)}
      >
        &times;
      </button>
    {:else}
      <button
        type="button"
        class="callout-row__ack-btn"
        aria-label={`Acknowledge: ${text.title}`}
        onclick={() => (open = true)}
      >
        Ack
      </button>
    {/if}
  </span>
  {/if}
  {#if error}<span class="callout-row__error" role="alert">{error}</span>{/if}
</p>

<style>
  /* Matches Overview's own .overview__attn-line look (baseline-aligned
     inline sentence, wrapping) so swapping the inline markup for this
     component is visually seamless. */
  .callout-row {
    display: flex;
    align-items: baseline;
    flex-wrap: wrap;
    gap: 0.5rem;
    margin: 0;
  }
  .callout-row__dot {
    width: 9px;
    height: 9px;
    border-radius: 50%;
    flex-shrink: 0;
    align-self: center;
  }
  .callout-row__title {
    font-weight: 600;
    font-size: 1.02rem;
    color: var(--ink);
  }
  a.callout-row__title {
    color: inherit;
  }
  .callout-row__detail {
    color: var(--ink-2);
    font-size: 0.88rem;
  }
  /* The ack control sits at the row's far edge (margin-left auto) so
     every row's control lands in the same column -- scannable, and well
     clear of the title link's own hit target. */
  .callout-row__ack {
    display: inline-flex;
    gap: 3px;
    margin-left: auto;
    align-self: center;
  }
  .callout-row__ack-btn {
    min-height: 22px;
    padding: 0 0.45rem;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--surface);
    color: var(--ink-2);
    font-family: var(--font-mono);
    font-size: 0.68rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    cursor: pointer;
  }
  .callout-row__ack-btn:hover:not(:disabled) {
    color: var(--ink);
    border-color: color-mix(in oklab, var(--ink) 25%, transparent);
  }
  .callout-row__ack-btn:disabled {
    opacity: 0.5;
    cursor: default;
  }
  .callout-row__ack-cancel {
    text-transform: none;
  }
  .callout-row__error {
    flex-basis: 100%;
    color: var(--status-critical);
    font-size: 0.78rem;
  }
</style>
