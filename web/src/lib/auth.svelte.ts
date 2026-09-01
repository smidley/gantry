// Auth store: the SPA-side half of the mandatory login. One boot fetch
// of /api/auth/status decides whether App renders the setup screen, the
// login screen, or the app; from then on, ANY api call answering 401
// flips the store back through the handler registered with api.ts -- the
// centralized "401 redirects to login" contract, so no view handles it
// itself. The current hash is untouched through a setup/login round-trip,
// so a deep link opened while logged out lands on its intended view once
// the credential is in.
import {
  fetchAuthStatus,
  postAuthSetup,
  postLogin,
  postLogout,
  setUnauthorizedHandler,
  type AuthState,
  type AuthStatus,
} from './api';
import { needsLogin, needsSetup } from './auth';

class AuthStore {
  // ready is false until the boot status fetch settles -- App renders
  // nothing gate-dependent before then, so a locked box never flashes
  // the dashboard shell.
  ready = $state(false);
  mode = $state<'auto' | 'proxy' | 'none'>('auto');
  // state defaults to 'authed' only as a pre-boot placeholder; `ready`
  // gates every consumer until the real status arrives.
  state = $state<AuthState>('authed');
  username = $state('');
  envManaged = $state(false);
  authenticated = $state(false);

  get needsSetup(): boolean {
    return needsSetup(this.state);
  }

  get needsLogin(): boolean {
    return needsLogin(this.state);
  }

  // init is called once from App's onMount. A failed status fetch still
  // marks ready (leaving the placeholder state): if the API is down
  // entirely, the views' own loading/error states are the right surface,
  // and the moment any later call 401s, onUnauthorized corrects the
  // picture.
  async init(): Promise<void> {
    setUnauthorizedHandler(() => this.onUnauthorized());
    await this.refresh();
    this.ready = true;
  }

  async refresh(): Promise<void> {
    try {
      this.apply(await fetchAuthStatus());
    } catch {
      // see init's doc: an unreachable status leaves the placeholder
    }
  }

  apply(st: AuthStatus): void {
    this.mode = st.mode;
    this.state = st.state;
    this.username = st.username ?? '';
    this.envManaged = st.env_managed;
    this.authenticated = st.authenticated;
  }

  // onUnauthorized: a 401 is proof the gate is on and this browser is not
  // authenticated. In mandatory mode that is almost always an expired or
  // revoked session -> the login screen; refresh() then corrects to the
  // setup screen in the rare case the credential was wiped entirely.
  onUnauthorized(): void {
    this.authenticated = false;
    this.state = 'login';
    void this.refresh();
  }

  // setup / login throw (AuthActionError for 401/429) for the form to
  // render; on success the cookie is already set by the response.
  async setup(username: string, password: string): Promise<void> {
    await postAuthSetup(username, password);
    this.authenticated = true;
    this.state = 'authed';
    this.username = username.trim();
  }

  async login(username: string, password: string): Promise<void> {
    await postLogin(username, password);
    this.authenticated = true;
    this.state = 'authed';
    this.username = username.trim();
  }

  async logout(): Promise<void> {
    try {
      await postLogout();
    } finally {
      // Locally logged out even if the request failed -- the cookie may
      // be gone or stale either way, and the login screen is the honest
      // place to be.
      this.authenticated = false;
      this.state = 'login';
    }
  }
}

export const auth = new AuthStore();
