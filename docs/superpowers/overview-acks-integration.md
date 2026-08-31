# Overview integration: clickable + acknowledgeable callouts (pending)

Everything below is **built, tested, and shipped dark** on `d2-overview`;
the one thing left is the wiring inside `web/src/views/Overview.svelte`,
which was frozen mid-hand-edit when this landed. Apply the patch below
once that file unfreezes.

## What already works without touching Overview

- Stopped containers no longer count as "needs you" (derivation change;
  the fleet sentence still carries "X running · Y stopped").
- Every anomaly's `describeAnomaly(...)` now carries `href`
  (`anomalyHref.ts`), so Overview's existing `{:else if text.href}`
  branch already links disk/array/source rows with no template change.
- Server: `overview_acks` table (migration 005), `GET/POST/DELETE
  /api/acks` (silences-style: no confirm header, not READ_ONLY-gated),
  Maintain prunes expired rows.
- `web/src/lib/acks.svelte.ts` store + `createAck`/`deleteAck`/`fetchAcks`
  in `api.ts`.
- `CalloutRow.svelte`: severity dot + linked title + inline reason + the
  1h/24h/7d ack control, routing by kind (alert callout -> existing
  silence API; frame-derived callout -> `/api/acks`).
- FleetStrip lays out as a fixed-pitch grid; BaySchematic sizes to
  content with a max (`width: fit-content; max-width: 100%`).

## The Overview.svelte patch (three edits)

```svelte
<script>
  // 1. imports + boot: load the ack list alongside everything else
  import CalloutRow from '../components/CalloutRow.svelte';
  import { acks } from '../lib/acks.svelte';
  // inside the existing onMount (or its own): acks.ensureLoaded();

  // 2. hand acks to the derivation (and drop the now-ignored stoppedCount)
  let overviewStatus = $derived(
    deriveOverviewStatus({
      unhealthyNames,
      arrayStarted: started,
      disks,
      sources: live.frame?.sources ?? {},
      alerts: live.frame?.alerts?.firing ?? [],
      acks: acks.list,
    }),
  );
</script>

<!-- 3. in the attention section, replace each hand-rolled row with -->
{#each overviewStatus.anomalies as anomaly, i (i)}
  <CalloutRow {anomaly} />
{/each}
```

`describeAnomaly` no longer needs importing once CalloutRow renders the
rows (CalloutRow calls it itself).

## After the patch

- Unskip the parked UI spec in `web/tests/acks.spec.ts` ("acking a
  Needs-a-look row hides it...").
- Re-balance the status band's own column split if desired: BaySchematic
  no longer claims full width, so `overview__status-visuals` can
  shrink-to-fit / share space differently -- that was deliberately left
  to the hand-edit in flight.
