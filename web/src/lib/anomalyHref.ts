// anomalyHref: "anything in the NEEDS YOU section needs to be clickable
// to get to information about that item" (Scott). Maps one Overview
// anomaly to the route that actually explains it -- eventHref's exact
// sibling, one level up: eventHref routes a raw event KIND, this routes
// an already-derived anomaly by its own discriminant.
//
// The mapping mirrors eventHref's destinations one-for-one where the
// concern overlaps: a container concern needs the container's own
// detail page; every disk/array concern lands on the one Storage page
// (still not addressable below the page level); an alert-backed callout
// lands on the Alerts view. source-critical is the one mapping with no
// eventHref sibling: CRITICAL_SOURCES (overviewStatus.ts) promotes only
// the docker collector, precisely because the fleet view depends on it
// -- so the fleet view (#/containers) is where that degradation
// actually shows, and where the row points. Any OTHER source that ever
// lands here routes nowhere (null -- a plain row), the same honest
// "no page exists for this" fallback eventHref returns for unroutable
// kinds. When insight callouts join the derivation they follow
// eventHref's own 'insight' mapping: #/insights.
import type { OverviewAnomaly } from './overviewStatus';

export function anomalyHref(a: OverviewAnomaly): string | null {
  switch (a.kind) {
    case 'unhealthy':
      return `#/containers/${encodeURIComponent(a.name)}`;
    case 'disk-usage':
    case 'disk-errors':
    case 'array-stopped':
      return '#/storage';
    case 'source-critical':
      return a.source === 'docker' ? '#/containers' : null;
    case 'alert':
      return '#/alerts';
  }
}
