import { afterEach, describe, expect, it, vi } from 'vitest';
import { fetchEvents, fetchSeries, fetchTop, fetchVersion, putSettings, streamLogs } from './api';

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

  it('fetchEvents rejects with an AbortError when its signal aborts before the response resolves', async () => {
    global.fetch = abortableFetchMock([]) as typeof fetch;
    const controller = new AbortController();
    const promise = fetchEvents({ limit: 200, signal: controller.signal });
    controller.abort();
    await expect(promise).rejects.toMatchObject({ name: 'AbortError' });
  });

  it('fetchEvents passes its signal through to fetch', async () => {
    const mock = abortableFetchMock([]);
    global.fetch = mock as typeof fetch;
    const controller = new AbortController();
    await fetchEvents({ kinds: ['container.oom'], limit: 200, signal: controller.signal });
    expect(mock).toHaveBeenCalledWith(
      expect.stringContaining('/api/events'),
      expect.objectContaining({ signal: controller.signal }),
    );
  });
});

describe('putSettings error shape', () => {
  const realFetch = global.fetch;
  afterEach(() => {
    global.fetch = realFetch;
  });

  it('throws a plain Error carrying the server message and per-field 400 detail', async () => {
    global.fetch = vi.fn(async () => ({
      ok: false,
      status: 400,
      statusText: 'Bad Request',
      json: async () => ({ error: 'invalid retention settings', fields: { r1_hours: 'must be between 1 and 168' } }),
    })) as unknown as typeof fetch;
    await expect(putSettings({ r1_hours: 0, r2_days: 7, r3_days: 30, size_cap_mb: 512 })).rejects.toMatchObject({
      message: 'invalid retention settings',
      fields: { r1_hours: 'must be between 1 and 168' },
    });
  });

  it('attaches envOverridden from a 409 conflict body', async () => {
    global.fetch = vi.fn(async () => ({
      ok: false,
      status: 409,
      statusText: 'Conflict',
      json: async () => ({ error: 'env-overridden fields cannot be changed here', env_overridden: ['r1_hours'] }),
    })) as unknown as typeof fetch;
    await expect(putSettings({ r1_hours: 5, r2_days: 7, r3_days: 30, size_cap_mb: 512 })).rejects.toMatchObject({
      message: 'env-overridden fields cannot be changed here',
      envOverridden: ['r1_hours'],
    });
  });

  it('resolves with the settings body on success', async () => {
    const success = { retention: { r1_hours: 48, r2_days: 30, r3_days: 395, size_cap_mb: 512 }, env_overridden: [] };
    global.fetch = vi.fn(async () => ({
      ok: true,
      status: 200,
      statusText: 'OK',
      json: async () => success,
    })) as unknown as typeof fetch;
    await expect(putSettings(success.retention)).resolves.toEqual(success);
  });
});

describe('fetchVersion', () => {
  const realFetch = global.fetch;
  afterEach(() => {
    global.fetch = realFetch;
  });

  it('resolves with the version response', async () => {
    global.fetch = vi.fn(async () => ({
      ok: true,
      status: 200,
      statusText: 'OK',
      json: async () => ({ version: 'dev' }),
    })) as unknown as typeof fetch;
    await expect(fetchVersion()).resolves.toEqual({ version: 'dev' });
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
