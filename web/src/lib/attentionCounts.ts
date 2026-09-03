// attentionCounts: the Overview's "needs you" surface as COUNTS rather
// than a list. Scott: "it doesn't create a list there, but instead has a
// count of items that need you. The user can click on the number and
// then be brought to a list of items that need attention. Alerts will go
// to the events page, and any container contentions will go to the
// insights page."
//
// So the attention section is two chips at most, over the exact same
// anomaly list the callout rows used to render one-per-item -- same
// derivation, same closed kind vocabulary, same acknowledgement filter
// (deriveOverviewStatus drops an acked row before this ever sees it, so
// an acked concern is missing from the COUNT exactly the way it used to
// be missing from the list). The headline's own "N things need you" is
// still anomalies.length, so the chips always sum to the headline.
//
// The split is the owner's own, and it is a two-way one: a contention is
// a diagnosis -- one container starving another over a shared resource,
// which is what the Insights view exists to explain -- and everything
// else in the vocabulary is something that fired or broke, which is what
// the Events page lists. There is deliberately no third bucket: a kind
// that is neither a finding nor obviously an alert (a stopped array, a
// degraded collector, a disk filling up) still belongs with the things
// that FIRED, because that is where its event landed.
import type { OverviewAnomaly } from './overviewStatus';

export type AttentionBucket = 'alerts' | 'contentions';

export interface AttentionChip {
  bucket: AttentionBucket;
  count: number;
  // noun: already singular/plural for count -- the chip renders the
  // number and this beside it.
  noun: string;
  href: string;
  // ariaLabel says the whole thing, including where the chip goes: the
  // visible chip is a bare number plus a one-word noun, which on its own
  // would tell a screen reader nothing about what activating it does.
  ariaLabel: string;
}

// attentionBucketFor maps one anomaly kind onto its chip. Exhaustive
// over OverviewAnomaly's own union by construction -- a new kind added
// to the derivation fails to type-check here until it is placed.
export function attentionBucketFor(a: OverviewAnomaly): AttentionBucket {
  switch (a.kind) {
    case 'insight':
      return 'contentions';
    case 'unhealthy':
    case 'disk-usage':
    case 'disk-errors':
    case 'array-stopped':
    case 'source-critical':
    case 'alert':
      return 'alerts';
  }
}

// BUCKETS carries each chip's fixed presentation, in render order:
// alerts first (something fired), contentions second (something was
// diagnosed).
const BUCKETS: { bucket: AttentionBucket; one: string; many: string; href: string; destination: string }[] = [
  { bucket: 'alerts', one: 'alert', many: 'alerts', href: '#/events', destination: 'view events' },
  { bucket: 'contentions', one: 'contention', many: 'contentions', href: '#/insights', destination: 'view insights' },
];

// alertsBucketAnomalies answers the counts pass's own open question --
// "its new home is a product call" -- for the alerts bucket: Events' own
// "Needs you" strip renders exactly this list, one CalloutRow per entry,
// the same per-item rendering Overview used to do before the chips
// replaced it. Same list attentionChips counts, same order, filtered
// down to the one bucket a page can actually show as rows; nothing here
// re-applies the ack filter -- an acked anomaly is already absent from
// the list deriveOverviewStatus handed back, the same way it's already
// absent from the chip's own count.
export function alertsBucketAnomalies(anomalies: OverviewAnomaly[]): OverviewAnomaly[] {
  return anomalies.filter((a) => attentionBucketFor(a) === 'alerts');
}

// attentionChips returns only the chips that have something to say -- a
// zero never renders, so an all-alerts page shows one chip rather than
// one chip and an empty second one claiming nothing is contended.
export function attentionChips(anomalies: OverviewAnomaly[]): AttentionChip[] {
  const counts: Record<AttentionBucket, number> = { alerts: 0, contentions: 0 };
  for (const a of anomalies) counts[attentionBucketFor(a)]++;

  const chips: AttentionChip[] = [];
  for (const b of BUCKETS) {
    const count = counts[b.bucket];
    if (count === 0) continue;
    const noun = count === 1 ? b.one : b.many;
    chips.push({
      bucket: b.bucket,
      count,
      noun,
      href: b.href,
      ariaLabel: `${count} ${noun} ${count === 1 ? 'needs' : 'need'} you, ${b.destination}`,
    });
  }
  return chips;
}
