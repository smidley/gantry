// Pure auth-gate decision logic -- the testable half of
// auth.svelte.ts, the livering/livering.svelte.ts split this repo uses
// everywhere a rune store has decisions worth pinning.
import { AuthActionError, type AuthState } from './api';

// The SPA renders one of three gate screens off the server's state:
// setup, login, or (authed/disabled) the app itself. These trivial
// predicates keep App's template readable and give the store one place
// to derive from.
export function needsSetup(state: AuthState): boolean {
  return state === 'setup';
}

export function needsLogin(state: AuthState): boolean {
  return state === 'login';
}

// loginErrorMessage maps a thrown login/setup/credential-change error to
// the line the form shows. 401 and 429 get fixed, friendly copy (the
// server's own messages are close, but the UI owns its voice); anything
// else surfaces the server's message so a real fault isn't masked.
export function loginErrorMessage(err: unknown): string {
  if (err instanceof AuthActionError) {
    if (err.status === 401) return 'Wrong username or password. Try again.';
    if (err.status === 429) return 'Too many attempts. Wait a minute, then try again.';
    return err.message;
  }
  return err instanceof Error ? err.message : 'Something went wrong. Try again.';
}

// credentialFormError validates a username + password + confirm form
// locally, before any request -- the setup screen and the Settings
// access card share it. passwordRequired is true on setup (a password
// must be chosen) and false when changing credentials (a blank password
// leaves the existing one, a username-only edit). It returns the first
// problem's message, or null when the form is good to submit.
export function credentialFormError(opts: {
  username: string;
  password: string;
  confirm: string;
  passwordRequired: boolean;
}): string | null {
  if (opts.username.trim() === '') {
    return 'Enter a username.';
  }
  const wantsPassword = opts.passwordRequired || opts.password !== '' || opts.confirm !== '';
  if (wantsPassword) {
    if (opts.password.length < 8) {
      return 'Password must be at least 8 characters.';
    }
    if (opts.password !== opts.confirm) {
      return "Passwords don't match.";
    }
  }
  return null;
}
