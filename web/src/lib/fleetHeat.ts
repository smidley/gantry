// fleetHeatVar: FleetStrip's per-unit heat mapping ("make it earn its
// space" -- tint each running container by its own live CPU host-share).
// Reuses metrics.ts's existing --seq-100..--seq-700 ramp rather than a
// new color scale.
import { seqStep } from './metrics';

// A container's host-share cpu.pct rarely climbs far past the low single
// digits even when genuinely busy (it's a fraction of the WHOLE machine,
// not of one core -- see fake.go's own cpu.pct doc). IDLE_PCT keeps
// near-zero usage reading as the strip's existing neutral tone instead of
// an unconditional blue tint; HOT_PCT is where the ramp is already fully
// spent -- one container legitimately holding a quarter of the host is
// plenty to call "working."
const FLEET_HEAT_IDLE_PCT = 1;
const FLEET_HEAT_HOT_PCT = 25;

// null means "render the caller's own default neutral" -- at/under the
// idle floor, or for non-finite input (no sample yet).
export function fleetHeatVar(cpuPct: number): string | null {
  if (!Number.isFinite(cpuPct) || cpuPct <= FLEET_HEAT_IDLE_PCT) return null;
  const scaled = ((cpuPct - FLEET_HEAT_IDLE_PCT) / (FLEET_HEAT_HOT_PCT - FLEET_HEAT_IDLE_PCT)) * 100;
  return seqStep(scaled);
}
