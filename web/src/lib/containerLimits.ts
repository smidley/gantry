// containerLimits: Container Detail's "Limits" facts line (Scott: "I
// want to know how much of the system resources it's consuming, AND how
// much of it's own allocated resources it's consuming" + "containers
// that have limits for things like cpu cores or memory should list
// them"). Pure derivation, same split as every other lib/*.ts helper.
import { fmtBytes, fmtCoresCeiling } from './format';

export interface ContainerLimitsInput {
  memLimitBytes?: number;
  cpuAllocCores?: number;
  pidsLimit?: number;
  // cpuset is ContainerDTO's own pre-formatted pin string ("0-5, 13-15")
  // -- already gated server-side to only narrow-below-host pins (see
  // docker.CPUSetPin's own doc), so '' or undefined both mean "no pin to
  // show" here.
  cpuset?: string;
}

// cpusetCoreCount counts how many distinct cores a canonical "0-5,
// 13-15" style pin string names. The Go passthrough (ContainerDTO.cpuset)
// always emits this exact comma-space/hyphen shape, so this only ever
// needs to parse that one canonical form, not arbitrary docker
// --cpuset-cpus syntax (unsorted lists, overlapping ranges, ...).
export function cpusetCoreCount(cpuset: string): number {
  let count = 0;
  for (const part of cpuset.split(',')) {
    const trimmed = part.trim();
    if (!trimmed) continue;
    const [lo, hi] = trimmed.split('-').map(Number);
    count += hi === undefined ? 1 : hi - lo + 1;
  }
  return count;
}

// limitsFactsParts renders one segment per resource that actually HAS a
// limit -- an empty array means fully unlimited, which callers render as
// no Limits line at all rather than an empty/placeholder one. Fixed
// order (memory, CPU, pids, cpuset) regardless of which are present,
// matching the ask's own worked example.
export function limitsFactsParts(input: ContainerLimitsInput): string[] {
  const parts: string[] = [];
  if (input.memLimitBytes !== undefined) parts.push(`memory ${fmtBytes(input.memLimitBytes)}`);
  if (input.cpuAllocCores !== undefined) parts.push(`CPU ${fmtCoresCeiling(input.cpuAllocCores)}`);
  if (input.pidsLimit !== undefined) parts.push(`pids ${Math.round(input.pidsLimit)}`);
  if (input.cpuset) parts.push(`pinned to ${cpusetCoreCount(input.cpuset)} cores: ${input.cpuset}`);
  return parts;
}
