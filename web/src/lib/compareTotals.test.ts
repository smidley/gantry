import { describe, expect, it } from 'vitest';
import { computeGroupTotals } from './compareTotals';
import type { ContainerDTO } from './api';

function container(metrics: Record<string, number>): ContainerDTO {
  return { state: 'running', health: 'healthy', image: 'demo:latest', icon: '', compose_project: '', metrics };
}

describe('computeGroupTotals', () => {
  it('sums each metric across every named member', () => {
    const containers: Record<string, ContainerDTO> = {
      'gridmind-api': container({ 'cpu.pct': 2.5, 'cpu.cores': 0.2, 'mem.bytes': 1e8, 'net.rx_bps': 1e5, 'net.tx_bps': 2e4 }),
      'gridmind-worker': container({ 'cpu.pct': 4.5, 'cpu.cores': 0.4, 'mem.bytes': 2e8, 'io.read_bps': 3e5, 'io.write_bps': 1e5 }),
    };

    const totals = computeGroupTotals(['gridmind-api', 'gridmind-worker'], containers);

    expect(totals.cpuPct).toBeCloseTo(7.0);
    expect(totals.cpuCores).toBeCloseTo(0.6);
    expect(totals.memBytes).toBe(3e8);
    expect(totals.netRxBps).toBe(1e5);
    expect(totals.netTxBps).toBe(2e4);
    expect(totals.ioReadBps).toBe(3e5);
    expect(totals.ioWriteBps).toBe(1e5);
  });

  it('treats a member missing from the containers map as contributing zero, not a throw', () => {
    const containers: Record<string, ContainerDTO> = { jellyfin: container({ 'cpu.pct': 4 }) };

    const totals = computeGroupTotals(['jellyfin', 'gone'], containers);

    expect(totals.cpuPct).toBe(4);
  });

  it('returns all-zero totals for an empty member list', () => {
    const totals = computeGroupTotals([], {});
    expect(totals).toEqual({
      cpuPct: 0,
      cpuCores: 0,
      memBytes: 0,
      memHostPct: undefined,
      netRxBps: 0,
      netTxBps: 0,
      ioReadBps: 0,
      ioWriteBps: 0,
    });
  });

  it('computes memHostPct from hostMemBytes when given', () => {
    const containers: Record<string, ContainerDTO> = {
      a: container({ 'mem.bytes': 2e9 }),
      b: container({ 'mem.bytes': 1e9 }),
    };

    const totals = computeGroupTotals(['a', 'b'], containers, 30e9);

    expect(totals.memBytes).toBe(3e9);
    expect(totals.memHostPct).toBeCloseTo(10.0);
  });

  it('leaves memHostPct undefined when hostMemBytes is not given', () => {
    const totals = computeGroupTotals(['a'], { a: container({ 'mem.bytes': 1e9 }) });
    expect(totals.memHostPct).toBeUndefined();
  });
});
