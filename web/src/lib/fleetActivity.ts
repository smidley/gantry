// fleetActivity: what makes a fleet block glow. "Glowing Container
// activity should be triggered by any metric that is above a threshold,
// not just CPU" (Scott) -- so a block's glow is now the MAX elevation
// across its whole metric set (cpu, memory, network, disk IO, GPU),
// and the hover label names whichever one is actually driving it, so a
// glowing block explains itself instead of just looking busy.
//
// ELEVATION is the one comparable number the five metrics are ranked
// on: how far a reading sits past its own idle floor, on the way to its
// own "fully working" point, clamped to 0..1. Zero means "not elevated
// at all" -- the calm default, and the reason an idle fleet stays
// completely still rather than shimmering at whatever its brightest
// container happens to be doing. That is also why the floors below are
// ABSOLUTE rather than relative-to-fleet: a relative rule always has a
// winner, so a perfectly quiet machine would still glow somewhere.
//
// Where the app already has a threshold band for a metric, the band IS
// the floor -- memory reuses thresholds.ts's own alert-rule-derived
// bands verbatim, so a glow and the alert engine can never disagree
// about whether a container's memory is elevated. The rate metrics
// (network, disk IO) and GPU busy have no band family at all
// (thresholds.ts says so explicitly), so each gets its own documented
// floor/full pair below, chosen so the numbers are explainable in one
// line to whoever is looking at the block.
import { fmtPct, fmtRate } from './format';
import { band } from './thresholds';
import type { Band } from './thresholds';
import { resourceMetricKeys } from './topFromFrame';

// The metric ids, in TOP_RESOURCES' own canonical order (topFromFrame.ts)
// -- reused rather than re-invented so the two "which resources does a
// container have" lists in this app can't drift, and so an exact tie in
// elevation resolves the same way the rest of the UI already orders
// these five.
export type ActivityMetric = 'cpu' | 'mem' | 'net' | 'io' | 'gpu';

export interface FleetActivityInput {
  // cpuPct: cpu.pct, host-share (a fraction of the WHOLE machine, not
  // of one core -- see the collector's cgroupv2.go doc).
  cpuPct?: number;
  // memLimitPct: mem.limit_pct, present only for a container that
  // actually has a memory limit. Preferred over memHostPct when both
  // exist -- a limit is the number the container itself will be killed
  // against.
  memLimitPct?: number;
  // memHostPct: mem.bytes as a percentage of host memory, for a
  // container with no limit of its own. The caller derives it (the
  // frame carries no host total directly -- resourceScaleMax's own
  // back-calculation).
  memHostPct?: number;
  // netBps/ioBps: net.rx_bps + net.tx_bps and io.read_bps +
  // io.write_bps, summed by the caller the same way topFromFrame's own
  // resourceMetricKeys sums them.
  netBps?: number;
  ioBps?: number;
  // gpuPct: the container's summed engine busy_pct (render/video/
  // video-enhance/copy), resourceMetricKeys' own gpu sum -- absent for
  // a container doing nothing on a GPU, or a host with none.
  gpuPct?: number;
}

export interface FleetActivity {
  // active: anything at all is elevated -- the block glows.
  active: boolean;
  // busy: the louder second tier, the point today's CPU-only rule
  // already called busy (see BUSY_ELEVATION).
  busy: boolean;
  // metric/value: which reading is driving the glow, and its raw value
  // in its own units. null/0 when nothing is elevated.
  metric: ActivityMetric | null;
  value: number;
  // elevation: the driving metric's own 0..1 reading (see the module doc).
  elevation: number;
  // label: the driving metric named with its value, for the hover
  // label -- "disk IO 84.0 MB/s". null when nothing is elevated.
  label: string | null;
}

// CPU: unchanged from the CPU-only rule this replaces, deliberately --
// cpu.pct > 1 (one percent of the whole host) is the same bar the
// Containers view's "Active now" filter and the fleet summary line
// already share, and 25% is where fleetHeat's own ramp was already
// fully spent: one container holding a quarter of the machine is
// plenty to call "working".
const CPU_IDLE_PCT = 1;
const CPU_HOT_PCT = 25;

// GPU: no band family exists for engine busy (thresholds.ts says so
// outright). 5% is above the idle sampling noise a shared GPU shows
// with nothing running on it; 60% engine-busy is a card genuinely
// working a transcode or an inference batch rather than ticking over.
const GPU_IDLE_PCT = 5;
const GPU_HOT_PCT = 60;

// Disk IO: bytes/sec, decimal units to match fmtRate (and docker's own
// convention). 2 MB/s clears routine logging and database heartbeat
// chatter, which is what would otherwise light up half a home-lab
// fleet permanently; 100 MB/s is roughly a spinning array disk's
// sequential ceiling, so a container sustaining it is saturating the
// device it is reading.
const IO_IDLE_BPS = 2e6;
const IO_HOT_BPS = 100e6;

// Network: same units. 1 MB/s clears dashboard polling, API chatter and
// health checks; 100 MB/s is a gigabit link essentially full (125 MB/s
// theoretical), which is the most a single container can usually take.
const NET_IDLE_BPS = 1e6;
const NET_HOT_BPS = 100e6;

// BUSY_ELEVATION is where the second, louder glow tier starts. The
// number is not arbitrary: it is exactly where the rule this replaces
// already put it -- cpu.pct 10 on the 1..25 ramp above is (10-1)/24 =
// 0.375 -- so a CPU-driven block glows and pulses today exactly as it
// did before any of the other four metrics could drive it.
const BUSY_ELEVATION = 0.375;

// BAND_ELEVATION maps a threshold band onto the same 0..1 scale the
// ramps produce. Stepped rather than smooth on purpose: the band IS the
// app's shared answer for how elevated one of these readings is, and
// interpolating between its own boundaries would invent precision the
// alert rules behind them don't have. 'normal' is not elevated at all.
const BAND_ELEVATION: Record<Band, number> = { normal: 0, warn: 1 / 3, serious: 2 / 3, critical: 1 };

// ramp: how far `value` sits past `idle` on the way to `hot`, clamped
// into 0..1. Non-finite input (no sample yet) and anything at or below
// the floor both read as not elevated -- an absent reading is never
// evidence of activity.
function ramp(value: number | undefined, idle: number, hot: number): number {
  if (value === undefined || !Number.isFinite(value) || value <= idle) return 0;
  return Math.min(1, (value - idle) / (hot - idle));
}

function bandElevation(family: 'container.mem_limit_pct' | 'host.mem', value: number | undefined): number {
  if (value === undefined || !Number.isFinite(value)) return 0;
  return BAND_ELEVATION[band(family, value)];
}

function labelFor(metric: ActivityMetric, value: number, ofLimit: boolean): string {
  switch (metric) {
    case 'cpu':
      return `CPU ${fmtPct(value)}`;
    case 'mem':
      return `memory ${fmtPct(value)} of ${ofLimit ? 'limit' : 'host'}`;
    case 'net':
      return `network ${fmtRate(value)}`;
    case 'io':
      return `disk IO ${fmtRate(value)}`;
    case 'gpu':
      return `GPU ${fmtPct(value)}`;
  }
}

const IDLE: FleetActivity = { active: false, busy: false, metric: null, value: 0, elevation: 0, label: null };

export function fleetActivity(input: FleetActivityInput): FleetActivity {
  // Memory prefers the container's OWN limit when it has one -- that is
  // the number it will actually be killed against -- and falls back to
  // its share of host memory when it doesn't.
  const ofLimit = input.memLimitPct !== undefined && Number.isFinite(input.memLimitPct);
  const memValue = ofLimit ? input.memLimitPct! : (input.memHostPct ?? 0);
  const memElevation = ofLimit
    ? bandElevation('container.mem_limit_pct', input.memLimitPct)
    : bandElevation('host.mem', input.memHostPct);

  // Fixed order (TOP_RESOURCES'), strictly-greater comparison: the max
  // wins, and an exact tie keeps the earlier metric rather than
  // flickering between two equally-elevated readings frame to frame.
  const candidates: { metric: ActivityMetric; value: number; elevation: number }[] = [
    { metric: 'cpu', value: input.cpuPct ?? 0, elevation: ramp(input.cpuPct, CPU_IDLE_PCT, CPU_HOT_PCT) },
    { metric: 'mem', value: memValue, elevation: memElevation },
    { metric: 'net', value: input.netBps ?? 0, elevation: ramp(input.netBps, NET_IDLE_BPS, NET_HOT_BPS) },
    { metric: 'io', value: input.ioBps ?? 0, elevation: ramp(input.ioBps, IO_IDLE_BPS, IO_HOT_BPS) },
    { metric: 'gpu', value: input.gpuPct ?? 0, elevation: ramp(input.gpuPct, GPU_IDLE_PCT, GPU_HOT_PCT) },
  ];

  let best = candidates[0];
  for (const c of candidates) {
    if (c.elevation > best.elevation) best = c;
  }
  if (best.elevation <= 0) return IDLE;

  return {
    active: true,
    busy: best.elevation >= BUSY_ELEVATION,
    metric: best.metric,
    value: best.value,
    elevation: best.elevation,
    label: labelFor(best.metric, best.value, ofLimit),
  };
}

// sumPresent sums whichever of `keys` the container actually reports,
// and reports undefined when it reports NONE of them -- topFromFrame's
// own sumPresentMetrics rule, for the same reason: a container with no
// GPU activity at all must read as "no sample", not as a real zero.
function sumPresent(metrics: Record<string, number>, keys: string[]): number | undefined {
  let total = 0;
  let present = false;
  for (const k of keys) {
    const v = metrics[k];
    if (v !== undefined) {
      total += v;
      present = true;
    }
  }
  return present ? total : undefined;
}

// activityInputFor is the ONE place a raw per-container metrics bag
// becomes this module's input -- the fleet strip's glow and the
// Containers view's "Active now" filter both go through it, so the two
// can never drift into separate definitions of the same word. The
// rate/GPU key lists come from topFromFrame's own resourceMetricKeys
// rather than being restated here, for the same reason.
//
// hostMemBytes (the host's total memory, which the frame carries only
// implicitly -- see resourceScaleMax's back-calculation) is optional:
// without it a container with no memory limit simply contributes no
// memory reading, rather than a wrong one.
export function activityInputFor(
  metrics: Record<string, number> | null | undefined,
  hostMemBytes?: number,
): FleetActivityInput {
  const m = metrics ?? {};
  const memBytes = m['mem.bytes'];
  const hostShare =
    memBytes !== undefined && hostMemBytes !== undefined && Number.isFinite(hostMemBytes) && hostMemBytes > 0
      ? (memBytes / hostMemBytes) * 100
      : undefined;
  return {
    cpuPct: m['cpu.pct'],
    memLimitPct: m['mem.limit_pct'],
    memHostPct: hostShare,
    netBps: sumPresent(m, resourceMetricKeys('net')),
    ioBps: sumPresent(m, resourceMetricKeys('io')),
    gpuPct: sumPresent(m, resourceMetricKeys('gpu')),
  };
}
