import { afterEach, describe, expect, it, vi } from 'vitest';
import { credentialFormError, loginErrorMessage, needsLogin, needsSetup } from './auth';
import {
  AuthActionError,
  fetchAcks,
  postAuthCredential,
  postAuthSetup,
  postLogin,
  postLogout,
  putSettings,
  setUnauthorizedHandler,
} from './api';

describe('screen predicates', () => {
  it('map the server state to exactly one gate screen', () => {
    expect(needsSetup('setup')).toBe(true);
    expect(needsSetup('login')).toBe(false);
    expect(needsLogin('login')).toBe(true);
    expect(needsLogin('setup')).toBe(false);
    // authed and disabled both render the app, neither gate screen.
    for (const s of ['authed', 'disabled'] as const) {
      expect(needsSetup(s)).toBe(false);
      expect(needsLogin(s)).toBe(false);
    }
  });
});

describe('loginErrorMessage', () => {
  it('maps 401 and 429 to fixed copy and passes other messages through', () => {
    expect(loginErrorMessage(new AuthActionError(401, 'invalid username or password'))).toBe(
      'Wrong username or password. Try again.',
    );
    expect(loginErrorMessage(new AuthActionError(429, 'too many attempts, wait a minute'))).toBe(
      'Too many attempts. Wait a minute, then try again.',
    );
    expect(loginErrorMessage(new AuthActionError(409, 'a credential is already set'))).toBe('a credential is already set');
    expect(loginErrorMessage(new Error('network down'))).toBe('network down');
    expect(loginErrorMessage('weird')).toBe('Something went wrong. Try again.');
  });
});

describe('credentialFormError', () => {
  it('requires a non-empty username', () => {
    expect(credentialFormError({ username: '   ', password: 'longenough', confirm: 'longenough', passwordRequired: true })).toBe(
      'Enter a username.',
    );
  });

  it('enforces the password rules when a password is required (setup)', () => {
    expect(credentialFormError({ username: 'admin', password: 'short', confirm: 'short', passwordRequired: true })).toBe(
      'Password must be at least 8 characters.',
    );
    expect(credentialFormError({ username: 'admin', password: 'longenough', confirm: 'different', passwordRequired: true })).toBe(
      "Passwords don't match.",
    );
    expect(credentialFormError({ username: 'admin', password: 'longenough', confirm: 'longenough', passwordRequired: true })).toBeNull();
  });

  it('treats a blank password as a username-only change when not required', () => {
    // Blank password + blank confirm: fine (keep the existing password).
    expect(credentialFormError({ username: 'admin', password: '', confirm: '', passwordRequired: false })).toBeNull();
    // But once either password field is touched, the rules apply again.
    expect(credentialFormError({ username: 'admin', password: 'short', confirm: '', passwordRequired: false })).toBe(
      'Password must be at least 8 characters.',
    );
    expect(credentialFormError({ username: 'admin', password: 'longenough', confirm: '', passwordRequired: false })).toBe(
      "Passwords don't match.",
    );
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

  it('does NOT fire the unauthorized handler for a wrong login', async () => {
    // A login 401 is the form's answer ("wrong username or password"), not
    // a session-level bounce -- redirecting would eat the error message.
    const mock = vi.fn(async () => jsonResponse(401, { error: 'invalid username or password' }));
    global.fetch = mock as unknown as typeof fetch;
    const onUnauthorized = vi.fn();
    setUnauthorizedHandler(onUnauthorized);

    await expect(postLogin('admin', 'nope')).rejects.toMatchObject({ status: 401, message: 'invalid username or password' });
    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  it('postAuthSetup and postAuthCredential surface status-carrying errors inline too', async () => {
    const onUnauthorized = vi.fn();
    setUnauthorizedHandler(onUnauthorized);

    global.fetch = vi.fn(async () => jsonResponse(409, { error: 'a credential is already set' })) as unknown as typeof fetch;
    await expect(postAuthSetup('admin', 'a-decent-password')).rejects.toMatchObject({ status: 409 });

    global.fetch = vi.fn(async () => jsonResponse(429, { error: 'too many attempts, wait a minute' })) as unknown as typeof fetch;
    await expect(postAuthCredential('cur', 'admin', 'next-password')).rejects.toMatchObject({ status: 429 });

    global.fetch = vi.fn(async () => jsonResponse(401, { error: 'invalid username or password' })) as unknown as typeof fetch;
    await expect(postAuthCredential('wrong', 'admin', '')).rejects.toMatchObject({ status: 401 });

    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  it('postAuthSetup, postLogin resolve on ok and postLogout tolerates 204', async () => {
    global.fetch = vi.fn(async () => jsonResponse(200, { ok: true })) as unknown as typeof fetch;
    await expect(postAuthSetup('admin', 'a-decent-password')).resolves.toBeUndefined();
    await expect(postLogin('admin', 'right-password')).resolves.toBeUndefined();

    global.fetch = vi.fn(async () => ({ ok: false, status: 204, statusText: 'No Content', json: async () => ({}) })) as unknown as typeof fetch;
    await expect(postLogout()).resolves.toBeUndefined();
  });
});
