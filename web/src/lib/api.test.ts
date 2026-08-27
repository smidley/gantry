import { afterEach, describe, expect, it, vi } from 'vitest';
import { fetchSeries, fetchTop, streamLogs } from './api';

// abortableFetchMock stands in for the real Fetch API's AbortSignal
// contract without a real network call: it resolves with `response`
// after a microtask tick, or rejects with a DOMException named
// "AbortError" the moment `signal` fires (or immediately, if it's
// already aborted before the call even starts) -- close enough to real
// fetch() behavior to prove fetchSeries/fetchTop thread `signal` through
// correctly and that an aborted call rejects in the shape callers detect
// via `err?.name === 'AbortError'`.
function abortableFetchMock(response: unknown) {
  return vi.fn((_url: string, init?: RequestInit) => {
    return new Promise((resolve, reject) => {
      const signal = init?.signal;
      if (signal?.aborted) {
        reject(new DOMException('aborted', 'AbortError'));
        return;
      }
      const onAbort = () => reject(new DOMException('aborted', 'AbortError'));
      signal?.addEventListener('abort', onAbort);
      queueMicrotask(() => {
        signal?.removeEventListener('abort', onAbort);
        resolve({
          ok: true,
          status: 200,
          statusText: 'OK',
          json: async () => response,
        });
      });
    });
  });
}

describe('fetchSeries / fetchTop abort support', () => {
  const realFetch = global.fetch;
  afterEach(() => {
    global.fetch = realFetch;
  });

  it('fetchSeries resolves normally when its signal is never aborted', async () => {
    global.fetch = abortableFetchMock([{ metric: 'cpu.pct', points: [] }]) as typeof fetch;
    const controller = new AbortController();
    const result = await fetchSeries({
      kind: 'container',
      entity: 'web',
      metrics: ['cpu.pct'],
      signal: controller.signal,
    });
    expect(result).toEqual([{ metric: 'cpu.pct', points: [] }]);
  });

  it('fetchSeries rejects with an AbortError when its signal aborts before the response resolves', async () => {
    global.fetch = abortableFetchMock([]) as typeof fetch;
    const controller = new AbortController();
    const promise = fetchSeries({ kind: 'container', entity: 'web', metrics: ['cpu.pct'], signal: controller.signal });
    controller.abort();
    await expect(promise).rejects.toMatchObject({ name: 'AbortError' });
  });

  it('fetchSeries passes its signal through to fetch', async () => {
    const mock = abortableFetchMock([]);
    global.fetch = mock as typeof fetch;
    const controller = new AbortController();
    await fetchSeries({ kind: 'container', entity: 'web', metrics: ['cpu.pct'], signal: controller.signal });
    expect(mock).toHaveBeenCalledWith(
      expect.stringContaining('/api/series'),
      expect.objectContaining({ signal: controller.signal }),
    );
  });

  it('fetchTop rejects with an AbortError when its signal aborts before the response resolves', async () => {
    global.fetch = abortableFetchMock([]) as typeof fetch;
    const controller = new AbortController();
    const promise = fetchTop({ resource: 'cpu', window: '1h', signal: controller.signal });
    controller.abort();
    await expect(promise).rejects.toMatchObject({ name: 'AbortError' });
  });

  it('fetchTop passes its signal through to fetch', async () => {
    const mock = abortableFetchMock([]);
    global.fetch = mock as typeof fetch;
    const controller = new AbortController();
    await fetchTop({ resource: 'cpu', window: '1h', signal: controller.signal });
    expect(mock).toHaveBeenCalledWith(
      expect.stringContaining('/api/top'),
      expect.objectContaining({ signal: controller.signal }),
    );
  });

  it('a request that resolves before any abort is unaffected by a signal that is never fired', async () => {
    global.fetch = abortableFetchMock([{ entity: 'web', value: 1 }]) as typeof fetch;
    const controller = new AbortController();
    const result = await fetchTop({ resource: 'cpu', window: '1h', signal: controller.signal });
    expect(result).toEqual([{ entity: 'web', value: 1 }]);
    expect(controller.signal.aborted).toBe(false);
  });
});

describe('streamLogs abort support', () => {
  const realFetch = global.fetch;
  afterEach(() => {
    global.fetch = realFetch;
  });

  it('passes its signal through to fetch', async () => {
    const encoder = new TextEncoder();
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(encoder.encode('hello\n'));
        controller.close();
      },
    });
    const mock = vi.fn(async (_url: string, _init?: RequestInit) => ({ ok: true, body: stream }));
    global.fetch = mock as unknown as typeof fetch;

    const controller = new AbortController();
    const gen = streamLogs('web', { follow: true, signal: controller.signal });
    const { value } = await gen.next();
    expect(value).toBe('hello\n');
    expect(mock).toHaveBeenCalledWith(
      expect.stringContaining('/api/containers/web/logs'),
      expect.objectContaining({ signal: controller.signal }),
    );
  });

  it('rejects immediately with an AbortError when the signal is already aborted', async () => {
    const mock = vi.fn((_url: string, init?: RequestInit) => {
      if (init?.signal?.aborted) return Promise.reject(new DOMException('aborted', 'AbortError'));
      return Promise.resolve({ ok: true, body: null });
    });
    global.fetch = mock as unknown as typeof fetch;

    const controller = new AbortController();
    controller.abort();
    const gen = streamLogs('web', { signal: controller.signal });
    await expect(gen.next()).rejects.toMatchObject({ name: 'AbortError' });
  });
});
