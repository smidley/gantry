// Auth store: the SPA-side half of the optional password gate. One
// boot fetch of /api/auth/status decides whether App renders the login
// screen or the app; from then on, ANY api call answering 401 flips the
// store back to "needs login" through the handler registered with
// api.ts -- the centralized "401 redirects to login" contract, so no
// view handles it itself. The current hash is untouched through a
// login round-trip, so a deep link opened while logged out lands on
// its intended view once the password is in.
import {
  fetchAuthStatus,
  postLogin,
  postLogout,
  setUnauthorizedHandler,
  type AuthStatus,
} from './api';
import { needsLogin, showsNoPasswordNudge } from './auth';

class AuthStore {
  // ready is false until the boot status fetch settles -- App renders
  // nothing gate-dependent before then, so a locked box never flashes
  // the dashboard shell.
  ready = $state(false);
  mode = $state<'auto' | 'proxy'>('auto');
  passwordSet = $state(false);
  envManaged = $state(false);
  authenticated = $state(false);

  get needsLogin(): boolean {
    return needsLogin({ mode: this.mode, passwordSet: this.passwordSet, authenticated: this.authenticated });
  }

  get showsNudge(): boolean {
    return showsNoPasswordNudge({ mode: this.mode, passwordSet: this.passwordSet });
  }

  // init is called once from App's onMount. A failed status fetch still
  // marks ready (with the open defaults): if the API is down entirely,
  // the views' own loading/error states are the right surface, not a
  // login screen for a gate we know nothing about -- and the moment any
  // later call 401s, onUnauthorized corrects the picture.
  async init(): Promise<void> {
    setUnauthorizedHandler(() => this.onUnauthorized());
    await this.refresh();
    this.ready = true;
  }

  async refresh(): Promise<void> {
    try {
      this.apply(await fetchAuthStatus());
    } catch {
      // see init's doc: unreachable status leaves the defaults standing
    }
  }

  apply(st: AuthStatus): void {
    this.mode = st.mode;
    this.passwordSet = st.password_set;
    this.envManaged = st.env_managed;
    this.authenticated = st.authenticated;
  }

  // onUnauthorized: a 401 is proof the gate is on, whatever the boot
  // status said -- someone may have set a password from another browser
  // while this tab sat open.
  onUnauthorized(): void {
    this.authenticated = false;
    this.passwordSet = true;
    this.mode = 'auto';
  }

  // login throws (AuthActionError for 401/429) for the form to render;
  // on success the cookie is already set by the response.
  async login(password: string): Promise<void> {
    await postLogin(password);
    this.authenticated = true;
    this.passwordSet = true;
  }

  async logout(): Promise<void> {
    try {
      await postLogout();
    } finally {
      // Locally logged out even if the request failed -- the cookie may
      // be gone or stale either way, and the login screen is the honest
      // place to be.
      this.authenticated = false;
    }
  }
}

export const auth = new AuthStore();
