import { describe, expect, it } from 'vitest';
import {
  applyMembershipHysteresis,
  canResortNow,
  createRankStabilityState,
  ENTRY_EXIT_TICKS,
  RESORT_INTERVAL_SEC,
  recordAndAverage,
  ROLLING_WINDOW_SEC,
  stableTopN,
} from './rankStability';

describe('recordAndAverage', () => {
  it('averages a single sample as itself', () => {
    const state = createRankStabilityState();
    expect(recordAndAverage(state, 'cpu', 'a', 10, 0)).toBe(10);
  });

  it('averages every sample still inside the window', () => {
    const state = createRankStabilityState();
    recordAndAverage(state, 'cpu', 'a', 10, 0);
    recordAndAverage(state, 'cpu', 'a', 20, 10);
    expect(recordAndAverage(state, 'cpu', 'a', 30, 20)).toBeCloseTo(20); // (10+20+30)/3
  });

  it('prunes samples older than the window before averaging', () => {
    const state = createRankStabilityState();
    recordAndAverage(state, 'cpu', 'a', 100, 0); // falls out of a 60s window by ts=65
    recordAndAverage(state, 'cpu', 'a', 10, 30);
    expect(recordAndAverage(state, 'cpu', 'a', 20, 65)).toBeCloseTo(15); // only the ts=30,65 samples remain
  });

  it('respects a custom window', () => {
    const state = createRankStabilityState();
    recordAndAverage(state, 'cpu', 'a', 100, 0);
    expect(recordAndAverage(state, 'cpu', 'a', 20, 5, 5)).toBeCloseTo(60); // ts=0 sample still exactly at the 5s cutoff-ish
  });

  it('tracks entities and metrics independently', () => {
    const state = createRankStabilityState();
    recordAndAverage(state, 'cpu', 'a', 10, 0);
    recordAndAverage(state, 'mem', 'a', 500, 0);
    expect(recordAndAverage(state, 'cpu', 'b', 999, 0)).toBe(999);
    expect(recordAndAverage(state, 'cpu', 'a', 10, 1)).toBeCloseTo(10);
    expect(recordAndAverage(state, 'mem', 'a', 500, 1)).toBeCloseTo(500);
  });
});

describe('canResortNow', () => {
  it('is true the first time (lastResortSec undefined)', () => {
    expect(canResortNow(undefined, 0)).toBe(true);
  });

  it('is false before the interval elapses', () => {
    expect(canResortNow(0, 5, 10)).toBe(false);
  });

  it('is true exactly at the interval boundary', () => {
    expect(canResortNow(0, 10, 10)).toBe(true);
  });

  it('defaults to RESORT_INTERVAL_SEC', () => {
    expect(canResortNow(0, RESORT_INTERVAL_SEC - 1)).toBe(false);
    expect(canResortNow(0, RESORT_INTERVAL_SEC)).toBe(true);
  });
});

describe('applyMembershipHysteresis', () => {
  it('is a pure removal filter -- it never returns an entity absent from `members`', () => {
    const state = createRankStabilityState();
    const survivors = applyMembershipHysteresis(state, 'cpu', ['a', 'b'], ['z', 'a', 'y', 'b', 'x'], 5);
    expect(survivors.every((e) => ['a', 'b'].includes(e))).toBe(true);
  });

  it('keeps everyone while all of `members` still rank inside the natural top-N', () => {
    const state = createRankStabilityState();
    expect(applyMembershipHysteresis(state, 'cpu', ['a', 'b', 'c'], ['a', 'b', 'c', 'd', 'e'], 5)).toEqual([
      'a',
      'b',
      'c',
    ]);
  });

  it('never evicts a member outranked for fewer than ENTRY_EXIT_TICKS calls', () => {
    const state = createRankStabilityState();
    expect(ENTRY_EXIT_TICKS).toBe(3); // this test's own tick count assumes 3
    const ranked = ['b', 'c', 'd', 'e', 'f', 'a']; // a (a member) has fallen out of the natural top-5
    let survivors = applyMembershipHysteresis(state, 'cpu', ['a', 'b', 'c', 'd', 'e'], ranked, 5);
    expect(survivors).toContain('a'); // tick 1 -- not enough yet
    survivors = applyMembershipHysteresis(state, 'cpu', survivors, ranked, 5);
    expect(survivors).toContain('a'); // tick 2
  });

  it('evicts on the tick a member has been outranked ENTRY_EXIT_TICKS calls running', () => {
    const state = createRankStabilityState();
    const ranked = ['b', 'c', 'd', 'e', 'f', 'a'];
    let members = ['a', 'b', 'c', 'd', 'e'];
    members = applyMembershipHysteresis(state, 'cpu', members, ranked, 5); // tick 1
    members = applyMembershipHysteresis(state, 'cpu', members, ranked, 5); // tick 2
    members = applyMembershipHysteresis(state, 'cpu', members, ranked, 5); // tick 3
    expect(members).toEqual(['b', 'c', 'd', 'e']); // a evicted, natural order preserved, nobody added
  });

  it('resets a fall streak the moment the member is back inside the natural top-N', () => {
    const state = createRankStabilityState();
    const outRanked = ['b', 'c', 'd', 'e', 'f', 'a'];
    const inRanked = ['a', 'b', 'c', 'd', 'e', 'f'];
    let members = ['a', 'b', 'c', 'd', 'e'];
    members = applyMembershipHysteresis(state, 'cpu', members, outRanked, 5); // tick 1 falling
    members = applyMembershipHysteresis(state, 'cpu', members, outRanked, 5); // tick 2 falling
    members = applyMembershipHysteresis(state, 'cpu', members, inRanked, 5); // back in -- streak resets
    members = applyMembershipHysteresis(state, 'cpu', members, outRanked, 5); // fresh tick 1
    members = applyMembershipHysteresis(state, 'cpu', members, outRanked, 5); // fresh tick 2, not 3
    expect(members).toContain('a'); // still a member -- the earlier two ticks never counted
  });

  it('keys bookkeeping by metric too, so a resource switch cannot cross-contaminate', () => {
    const state = createRankStabilityState();
    const ranked = ['b', 'c', 'd', 'e', 'f', 'a'];
    applyMembershipHysteresis(state, 'cpu', ['a', 'b', 'c', 'd', 'e'], ranked, 5);
    // 'mem' has never seen this fall streak -- unaffected.
    expect(applyMembershipHysteresis(state, 'mem', ['x', 'y'], ['x', 'y'], 5)).toEqual(['x', 'y']);
  });
});

describe('stableTopN', () => {
  function row(entity: string, value: number, extra: Record<string, unknown> = {}) {
    return { entity, value, ...extra };
  }

  it('shows something immediately on the first tick', () => {
    const state = createRankStabilityState();
    const rows = [row('a', 10), row('b', 30), row('c', 20)];
    expect(stableTopN(rows, state, 'cpu', 2, 0).map((r) => r.entity)).toEqual(['b', 'c']);
  });

  it('breaks a tie by entity name ascending, deterministically', () => {
    const state = createRankStabilityState();
    const rows = [row('zeta', 10), row('alpha', 10)];
    expect(stableTopN(rows, state, 'cpu', 2, 0).map((r) => r.entity)).toEqual(['alpha', 'zeta']);
  });

  it('holds order steady across noisy ticks among near-tied entities (the real-box scenario)', () => {
    const state = createRankStabilityState();
    const names = ['n1', 'n2', 'n3', 'n4', 'n5', 'n6', 'n7', 'n8', 'n9', 'n10', 'n11', 'n12'];
    let displayed: string[] | null = null;
    let resortCount = 0;
    for (let tick = 0; tick < 30; tick++) {
      const nowSec = tick * 2; // real 2s server cadence
      // Every container hovers around 0.1, jittering independently each
      // tick -- exactly the dozen-near-0.1%-ties real-box symptom.
      const rows = names.map((n) => row(n, 0.1 + (Math.sin(tick * 7 + n.length) + 1) * 0.05));
      const out = stableTopN(rows, state, 'top', 5, nowSec).map((r) => r.entity);
      if (displayed && JSON.stringify(out) !== JSON.stringify(displayed)) resortCount++;
      displayed = out;
    }
    // The old code re-sorted (and often changed MEMBERSHIP) on essentially
    // every one of the 30 ticks. A rolling average this heavily damps
    // per-tick jitter, and evictions need both a 3-tick streak and a
    // 10s cooldown -- at most a handful of adopted changes over a 58s run.
    expect(resortCount).toBeLessThanOrEqual(6);
  });

  it('never shows more than `limit` rows, even mid-eviction', () => {
    const state = createRankStabilityState();
    const names = ['a', 'b', 'c', 'd', 'e', 'f', 'g', 'h'];
    for (let tick = 0; tick < 20; tick++) {
      // A different random ranking every tick -- heavy simultaneous
      // churn, the exact pattern that ballooned the old withGracePeriod
      // implementation past its own 2x-limit bound.
      const shuffled = [...names].sort(() => Math.random() - 0.5);
      const rows = shuffled.map((n, i) => row(n, shuffled.length - i));
      const out = stableTopN(rows, state, 'cpu', 4, tick * 2);
      expect(out.length).toBeLessThanOrEqual(4);
    }
  });

  it('plays out a genuine, sustained rank change as two separate steps: an eviction, then a fill', () => {
    const state = createRankStabilityState();
    const base = () => [row('a', 10), row('b', 9), row('c', 8), row('d', 7), row('e', 6), row('f', 1)];
    // Ticks at nowSec=0,2: settle the initial top-5 (a..e), f trailing.
    stableTopN(base(), state, 'top', 5, 0);
    stableTopN(base(), state, 'top', 5, 2);
    // From nowSec=4 on, f genuinely overtakes e and stays there.
    const shifted = () => [row('a', 10), row('b', 9), row('c', 8), row('d', 7), row('e', 1), row('f', 20)];
    let out = stableTopN(shifted(), state, 'top', 5, 4); // hysteresis tick 1 for e's fall
    expect(out.map((r) => r.entity)).toEqual(['a', 'b', 'c', 'd', 'e']); // nothing settled yet
    out = stableTopN(shifted(), state, 'top', 5, 6); // tick 2
    expect(out.map((r) => r.entity)).toEqual(['a', 'b', 'c', 'd', 'e']);
    out = stableTopN(shifted(), state, 'top', 5, 8); // tick 3 -- e evicted: a pure outro
    expect(out.map((r) => r.entity)).toEqual(['a', 'b', 'c', 'd']);
    out = stableTopN(shifted(), state, 'top', 5, 10); // the very next call -- f fills the free slot: a pure intro
    expect(out.map((r) => r.entity)).toEqual(['f', 'a', 'b', 'c', 'd']); // f ranks first: 4 ticks at 20 already lead a's steady 10
  });

  it('never asks for an intro and an outro in the same adopted update', () => {
    const state = createRankStabilityState();
    const base = () => [row('a', 10), row('b', 9), row('c', 8), row('d', 7), row('e', 6), row('f', 1)];
    stableTopN(base(), state, 'top', 5, 0);
    stableTopN(base(), state, 'top', 5, 2);
    const shifted = () => [row('a', 10), row('b', 9), row('c', 8), row('d', 7), row('e', 1), row('f', 20)];
    stableTopN(shifted(), state, 'top', 5, 4);
    stableTopN(shifted(), state, 'top', 5, 6);
    // The eviction call (nowSec=8) drops a member without adding one;
    // the call after (nowSec=10) adds one without dropping one --
    // confirmed live: asking Svelte's keyed each-block to do both in the
    // SAME reconciliation could leave one row permanently stuck (frozen
    // invisible mid-intro, or frozen visible mid-outro).
    const evicted = stableTopN(shifted(), state, 'top', 5, 8).map((r) => r.entity);
    const filled = stableTopN(shifted(), state, 'top', 5, 10).map((r) => r.entity);
    expect(evicted.length).toBe(4); // pure outro: nothing added alongside the drop
    expect(filled.length).toBe(5); // pure intro next call: nothing dropped alongside the add
    expect(filled.filter((e) => !evicted.includes(e))).toEqual(['f']); // exactly one addition, no re-eviction
  });

  it('keeps values live every tick even while membership is frozen', () => {
    const state = createRankStabilityState();
    stableTopN([row('a', 10), row('b', 5)], state, 'top', 5, 0);
    const out = stableTopN([row('a', 11), row('b', 6)], state, 'top', 5, 1);
    expect(out.map((r) => r.value)).toEqual([11, 6]); // fresh values, same order
  });

  it('never reorders a linkable:false row -- it stays pinned wherever the caller put it', () => {
    const state = createRankStabilityState();
    const rows = [row('quiet', 1), row('Unattributed (host)', 999, { linkable: false })];
    expect(stableTopN(rows, state, 'cpu', 5, 0).map((r) => r.entity)).toEqual(['quiet', 'Unattributed (host)']);
  });

  it('keeps a live-refreshed pinned row even while member order is frozen', () => {
    const state = createRankStabilityState();
    stableTopN([row('quiet', 1), row('Unattributed (host)', 999, { linkable: false })], state, 'cpu', 5, 0);
    const out = stableTopN(
      [row('quiet', 1), row('Unattributed (host)', 1234, { linkable: false })],
      state,
      'cpu',
      5,
      1,
    );
    expect(out[out.length - 1]).toMatchObject({ entity: 'Unattributed (host)', value: 1234 });
  });

  it('keys every piece of bookkeeping by metric, so a resource switch starts fresh', () => {
    const state = createRankStabilityState();
    stableTopN([row('a', 100)], state, 'cpu', 5, 0);
    // 'mem' has never seen 'a' -- must rank purely off its own fresh value.
    const out = stableTopN([row('a', 1), row('b', 5)], state, 'mem', 5, 0);
    expect(out.map((r) => r.entity)).toEqual(['b', 'a']);
  });

  it('exports a positive ROLLING_WINDOW_SEC for callers that want to reason about it', () => {
    expect(ROLLING_WINDOW_SEC).toBeGreaterThan(0);
  });
});
