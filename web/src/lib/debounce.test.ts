import { afterEach, describe, expect, it, vi } from 'vitest';
import { debounce } from './debounce';

afterEach(() => {
  vi.useRealTimers();
});

describe('debounce', () => {
  it('waits the full delay before calling fn', () => {
    vi.useFakeTimers();
    const calls: string[] = [];
    const d = debounce((v: string) => calls.push(v), 300);

    d('a');
    vi.advanceTimersByTime(299);
    expect(calls).toEqual([]);
    vi.advanceTimersByTime(1);
    expect(calls).toEqual(['a']);
  });

  it('coalesces rapid calls into one, using only the last call\'s args', () => {
    vi.useFakeTimers();
    const calls: string[] = [];
    const d = debounce((v: string) => calls.push(v), 300);

    d('a');
    vi.advanceTimersByTime(100);
    d('b');
    vi.advanceTimersByTime(100);
    d('c'); // each call resets the timer -- 'a' and 'b' never fire
    vi.advanceTimersByTime(299);
    expect(calls).toEqual([]);
    vi.advanceTimersByTime(1);
    expect(calls).toEqual(['c']);
  });

  it('fires again for a later call spaced past the delay', () => {
    vi.useFakeTimers();
    const calls: string[] = [];
    const d = debounce((v: string) => calls.push(v), 300);

    d('a');
    vi.advanceTimersByTime(300);
    expect(calls).toEqual(['a']);

    d('b');
    vi.advanceTimersByTime(300);
    expect(calls).toEqual(['a', 'b']);
  });

  it('passes through multiple arguments', () => {
    vi.useFakeTimers();
    const calls: [string, number][] = [];
    const d = debounce((s: string, n: number) => calls.push([s, n]), 50);

    d('x', 1);
    vi.advanceTimersByTime(50);
    expect(calls).toEqual([['x', 1]]);
  });
});
