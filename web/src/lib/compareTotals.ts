// Pure totals math for the compare view's group summary row: "how much
// is this whole team using, together" -- a plain per-metric sum across
// the compared members' own current metrics, no window/aggregation
// involved (this is always a LIVE, current-instant figure, independent
// of whichever history range the charts below are showing -- same split
// ContainerDetail's own header facts already have against its charts).
import type { ContainerDTO } from './api';

export interface GroupTotals {
  cpuPct: number; // Sigma cpu.pct (host-share %) across every member
  cpuCores: number; // Sigma cpu.cores -- the quiet "(approx N cores)" annotation, same pairing format.ts's fmtCores backs elsewhere
  memBytes: number; // Sigma mem.bytes
  memHostPct: number | undefined; // memBytes as a % of the host's own total memory, when that total is known
  netRxBps: number;
  netTxBps: number;
  ioReadBps: number;
  ioWriteBps: number;
}

// computeGroupTotals sums each named member's own current metrics. A name
// absent from `containers` (removed from the fleet since a compare URL
// was bookmarked, or simply not loaded yet) contributes nothing to any
// sum, rather than throwing -- same "missing degrades to a harmless
// placeholder" rule format.ts's own fmt* functions follow; callers that
// want to EXCLUDE a gone member entirely (rather than silently treating
// it as zero) should filter `names` through compareRoute.ts's own
// knownCompareNames first.
//
// hostMemBytes is the host's own total memory in bytes (topFromFrame.ts's
// resourceScaleMax('mem', frame) already derives this from the host
// tile's used-bytes/used-pct pair) -- optional, since a snapshot that
// hasn't reported a host mem reading yet has no such total; memHostPct
// comes back undefined in that case rather than a divide-by-zero NaN.
export function computeGroupTotals(
  names: string[],
  containers: Record<string, ContainerDTO>,
  hostMemBytes?: number,
): GroupTotals {
  let cpuPct = 0;
  let cpuCores = 0;
  let memBytes = 0;
  let netRxBps = 0;
  let netTxBps = 0;
  let ioReadBps = 0;
  let ioWriteBps = 0;

  for (const name of names) {
    const m = containers[name]?.metrics;
    if (!m) continue;
    cpuPct += m['cpu.pct'] ?? 0;
    cpuCores += m['cpu.cores'] ?? 0;
    memBytes += m['mem.bytes'] ?? 0;
    netRxBps += m['net.rx_bps'] ?? 0;
    netTxBps += m['net.tx_bps'] ?? 0;
    ioReadBps += m['io.read_bps'] ?? 0;
    ioWriteBps += m['io.write_bps'] ?? 0;
  }

  return {
    cpuPct,
    cpuCores,
    memBytes,
    memHostPct: hostMemBytes ? (memBytes / hostMemBytes) * 100 : undefined,
    netRxBps,
    netTxBps,
    ioReadBps,
    ioWriteBps,
  };
}
