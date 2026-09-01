// Pure auth-gate decision logic -- the testable half of
// auth.svelte.ts, the livering/livering.svelte.ts split this repo uses
// everywhere a rune store has decisions worth pinning.
import { AuthActionError } from './api';

export interface AuthGateState {
  mode: 'auto' | 'proxy';
  passwordSet: boolean;
  authenticated: boolean;
}

// needsLogin: the login screen shows exactly when the server would 401
// us -- auto mode, a password configured, no live session. Proxy mode
// never gates (the reverse proxy already authenticated this request or
// it wouldn't have arrived), and no-password is the open zero-config
// default.
export function needsLogin(s: AuthGateState): boolean {
  return s.mode === 'auto' && s.passwordSet && !s.authenticated;
}

// showsNoPasswordNudge: the Settings access card's quiet warning. Only
// in auto mode -- a proxy-mode install solved authentication one layer
// up, and nagging it would train people to ignore the nudge.
export function showsNoPasswordNudge(s: { mode: 'auto' | 'proxy'; passwordSet: boolean }): boolean {
  return s.mode === 'auto' && !s.passwordSet;
}

// loginErrorMessage maps a thrown login/password-change error to the
// line the form shows. 401 and 429 get fixed, friendly copy (the
// server's own messages are close, but the UI owns its voice); anything
// else surfaces the server's message so a real fault isn't masked.
export function loginErrorMessage(err: unknown): string {
  if (err instanceof AuthActionError) {
    if (err.status === 401) return 'Wrong password. Try again.';
    if (err.status === 429) return 'Too many attempts. Wait a minute, then try again.';
    return err.message;
  }
  return err instanceof Error ? err.message : 'Something went wrong. Try again.';
}
