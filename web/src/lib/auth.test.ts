import { afterEach, describe, expect, it, vi } from 'vitest';
import { loginErrorMessage, needsLogin, showsNoPasswordNudge } from './auth';
import {
  AuthActionError,
  fetchAcks,
  postAuthDisable,
  postAuthPassword,
  postLogin,
  postLogout,
  putSettings,
  setUnauthorizedHandler,
} from './api';

describe('needsLogin', () => {
  it('gates exactly when the server would 401: auto mode, password set, no session', () => {
    expect(needsLogin({ mode: 'auto', passwordSet: true, authenticated: false })).toBe(true);
    expect(needsLogin({ mode: 'auto', passwordSet: true, authenticated: true })).toBe(false);
    expect(needsLogin({ mode: 'auto', passwordSet: false, authenticated: false })).toBe(false);
    // Proxy mode never gates client-side -- the reverse proxy already
    // authenticated the request or it wouldn't have arrived.
    expect(needsLogin({ mode: 'proxy', passwordSet: true, authenticated: false })).toBe(false);
  });
});

describe('showsNoPasswordNudge', () => {
  it('nudges only an open auto-mode install', () => {
    expect(showsNoPasswordNudge({ mode: 'auto', passwordSet: false })).toBe(true);
    expect(showsNoPasswordNudge({ mode: 'auto', passwordSet: true })).toBe(false);
    expect(showsNoPasswordNudge({ mode: 'proxy', passwordSet: false })).toBe(false);
  });
});

describe('loginErrorMessage', () => {
  it('maps 401 and 429 to fixed copy and passes other messages through', () => {
    expect(loginErrorMessage(new AuthActionError(401, 'invalid password'))).toBe('Wrong password. Try again.');
    expect(loginErrorMessage(new AuthActionError(429, 'too many attempts, wait a minute'))).toBe(
      'Too many attempts. Wait a minute, then try again.',
    );
    expect(loginErrorMessage(new AuthActionError(409, 'no password is set'))).toBe('no password is set');
    expect(loginErrorMessage(new Error('network down'))).toBe('network down');
    expect(loginErrorMessage('weird')).toBe('Something went wrong. Try again.');
  });
});

// jsonResponse builds a minimal fetch-Response stand-in.
function jsonResponse(status: number, body: unknown) {
  return {
    ok: status >= 200 && status < 300,
    status,
    statusText: String(status),
    json: async () => body,
  };
}

describe('api request plumbing', () => {
  const realFetch = global.fetch;
  afterEach(() => {
    global.fetch = realFetch;
    setUnauthorizedHandler(null);
  });

  it('stamps X-Requested-With: gantry on mutating requests', async () => {
    const mock = vi.fn(async () => jsonResponse(200, { retention: {}, env_overridden: [] }));
    global.fetch = mock as unknown as typeof fetch;

    await putSettings({ r1_hours: 48, r2_days: 14, r3_days: 365, size_cap_mb: 512 });

    const [, init] = mock.mock.calls[0] as unknown as [string, RequestInit];
    expect(new Headers(init.headers).get('X-Requested-With')).toBe('gantry');
  });

  it('leaves GETs header-free (nothing mutating, no preflight to force)', async () => {
    const mock = vi.fn(async () => jsonResponse(200, { acks: [] }));
    global.fetch = mock as unknown as typeof fetch;

    await fetchAcks();

    const [, init] = mock.mock.calls[0] as unknown as [string, RequestInit | undefined];
    expect(new Headers(init?.headers).get('X-Requested-With')).toBeNull();
  });

  it('fires the unauthorized handler on any 401', async () => {
    const mock = vi.fn(async () => jsonResponse(401, { error: 'authentication required' }));
    global.fetch = mock as unknown as typeof fetch;
    const onUnauthorized = vi.fn();
    setUnauthorizedHandler(onUnauthorized);

    await expect(fetchAcks()).rejects.toThrow();
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
  });

  it('does NOT fire the unauthorized handler for a wrong login password', async () => {
    // A login 401 is the form's answer ("wrong password"), not a
    // session-level bounce -- redirecting would eat the error message.
    const mock = vi.fn(async () => jsonResponse(401, { error: 'invalid password' }));
    global.fetch = mock as unknown as typeof fetch;
    const onUnauthorized = vi.fn();
    setUnauthorizedHandler(onUnauthorized);

    await expect(postLogin('nope')).rejects.toMatchObject({ status: 401, message: 'invalid password' });
    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  it('postAuthPassword and postAuthDisable surface status-carrying errors inline too', async () => {
    const onUnauthorized = vi.fn();
    setUnauthorizedHandler(onUnauthorized);

    global.fetch = vi.fn(async () => jsonResponse(429, { error: 'too many attempts, wait a minute' })) as unknown as typeof fetch;
    await expect(postAuthPassword('cur', 'next-password')).rejects.toMatchObject({ status: 429 });

    global.fetch = vi.fn(async () => jsonResponse(401, { error: 'invalid password' })) as unknown as typeof fetch;
    await expect(postAuthDisable('wrong')).rejects.toMatchObject({ status: 401 });

    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  it('postLogin resolves on ok and postLogout tolerates 204', async () => {
    global.fetch = vi.fn(async () => jsonResponse(200, { ok: true })) as unknown as typeof fetch;
    await expect(postLogin('right-password')).resolves.toBeUndefined();

    global.fetch = vi.fn(async () => ({ ok: false, status: 204, statusText: 'No Content', json: async () => ({}) })) as unknown as typeof fetch;
    await expect(postLogout()).resolves.toBeUndefined();
  });
});
