<script>
  import { onMount } from 'svelte';
  import { auth } from '../lib/auth.svelte';
  import { postAuthCredential } from '../lib/api';
  import { credentialFormError, loginErrorMessage } from '../lib/auth';
  onMount(() => { auth.refresh(); });
  // --- Access (mandatory login) -----------------------------------------
  // The card runs off the auth store (refreshed on mount so it reflects
  // reality, not the boot snapshot). Auth is always on now; this card
  // changes the username and/or password. The change round-trips through
  // /api/auth/credential: it signs out every other session (the server's
  // own contract) and this browser keeps a fresh cookie from the same
  // response, so it never logs the user out of the tab they're standing
  // in. A blank new password is a username-only change. Passwords live in
  // these fields only for the duration of the request and are never
  // echoed anywhere.
  let pwCurrent = $state('');
  let newUsername = $state('');
  let pwNew = $state('');
  let pwConfirm = $state('');
  let pwSaving = $state(false);
  let pwError = $state(null);
  let pwSuccess = $state(null);

  // Seed the username field from the current one the first time the store
  // reports it (the boot status arrives after this component mounts),
  // then leave the user's edits alone.
  let usernameSeeded = false;
  $effect(() => {
    if (!usernameSeeded && auth.username) {
      newUsername = auth.username;
      usernameSeeded = true;
    }
  });

  async function submitCredential(e) {
    e.preventDefault();
    pwError = null;
    pwSuccess = null;
    const problem = credentialFormError({
      username: newUsername,
      password: pwNew,
      confirm: pwConfirm,
      passwordRequired: false,
    });
    if (problem) {
      pwError = problem;
      return;
    }
    pwSaving = true;
    try {
      await postAuthCredential(pwCurrent, newUsername, pwNew);
      pwSuccess = 'Login updated. Every other session was signed out.';
      pwCurrent = '';
      pwNew = '';
      pwConfirm = '';
      await auth.refresh();
    } catch (err) {
      pwError = loginErrorMessage(err);
    } finally {
      pwSaving = false;
    }
  }

  function logout() {
    // App's own gate effect tears down the SSE connection and swaps to
    // the login screen the moment authenticated flips.
    auth.logout();
  }

</script>
  <div id="settings-access" class="card settings-access">
    <span class="microlabel">Access</span>
    {#if auth.mode === 'proxy'}
      <p class="settings-access__note">
        Authentication is handled by your reverse proxy (GANTRY_AUTH=proxy). Gantry's built-in login is off, and
        credentials are managed at the proxy, not here.
      </p>
    {:else if auth.mode === 'none'}
      <p class="settings-access__note">
        Authentication is turned off (GANTRY_AUTH=none) — anyone who can reach this dashboard can view and manage it.
        Remove that variable to require a login again.
      </p>
    {:else}
      <p class="settings-access__note">
        Signed in as <strong>{auth.username}</strong>. Sessions expire after 8 hours idle or 24 hours total. Closing a browser may preserve its session when tabs are restored.
      </p>
      {#if auth.envManaged}
        <p class="settings-access__note settings-access__note--env">
          The login comes from the GANTRY_USERNAME / GANTRY_PASSWORD container variables at every start — a change made
          here lasts only until the next restart re-applies them. Update the variables in the container template to make
          a change stick. Removing the variables does <em>not</em> turn authentication off.
        </p>
      {/if}

      <form class="settings-access__form" onsubmit={submitCredential} novalidate>
        <label class="settings-access__field">
          <span class="microlabel">Current password</span>
          <input type="password" bind:value={pwCurrent} autocomplete="current-password" disabled={pwSaving} />
        </label>
        <label class="settings-access__field">
          <span class="microlabel">Username</span>
          <input
            type="text"
            bind:value={newUsername}
            autocomplete="username"
            autocapitalize="none"
            autocorrect="off"
            spellcheck="false"
            disabled={pwSaving}
          />
        </label>
        <label class="settings-access__field">
          <span class="microlabel">New password <span class="settings-access__optional">— leave blank to keep</span></span>
          <input type="password" bind:value={pwNew} autocomplete="new-password" disabled={pwSaving} />
        </label>
        <label class="settings-access__field">
          <span class="microlabel">Confirm new password</span>
          <input type="password" bind:value={pwConfirm} autocomplete="new-password" disabled={pwSaving} />
        </label>
        <div class="settings-access__actions">
          <button type="submit" class="settings-access__save" disabled={pwSaving}>
            {pwSaving ? 'Saving…' : 'Update login'}
          </button>
          <span class="microlabel">Updating your login signs out every other session.</span>
        </div>
        {#if pwError}<p class="microlabel settings-access__error" role="alert">{pwError}</p>{/if}
        {#if pwSuccess}<p class="microlabel settings-access__success">{pwSuccess}</p>{/if}
      </form>

      <div class="settings-access__session-row">
        <button type="button" class="settings-access__secondary" onclick={logout}>Log out</button>
      </div>
    {/if}
  </div>


<style>
  .settings-access {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
  }
  .settings-access__note {
    margin: 0;
    font-size: 0.82rem;
    color: var(--ink-2);
  }
  .settings-access__optional {
    color: var(--ink-2);
    font-weight: 400;
    text-transform: none;
    letter-spacing: 0;
  }
  .settings-access__note--env {
    padding: 0.5rem 0.6rem;
    border-radius: 8px;
    background: color-mix(in oklab, var(--status-warning) 10%, transparent);
    color: var(--ink);
  }
  .settings-access__form {
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
  }
  .settings-access__field {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    max-width: 18rem;
  }
  .settings-access__field input {
    min-height: 40px;
    padding: 0 0.75rem;
    border-radius: 6px;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: var(--surface);
    color: var(--ink);
    font-size: 0.9rem;
  }
  .settings-access__field input:disabled {
    opacity: 0.6;
  }
  .settings-access__actions {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    flex-wrap: wrap;
  }
  .settings-access__save {
    min-height: 40px;
    padding: 0 1.25rem;
    border-radius: 6px;
    border: 1px solid var(--series-1);
    background: color-mix(in oklab, var(--series-1) 15%, transparent);
    color: var(--series-1);
    font-size: 0.85rem;
    font-weight: 500;
    cursor: pointer;
  }
  .settings-access__secondary {
    min-height: 40px;
    padding: 0 0.9rem;
    border-radius: 6px;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: transparent;
    color: var(--ink);
    font-size: 0.8rem;
    cursor: pointer;
  }
  .settings-access__save:disabled,
  .settings-access__secondary:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }
  .settings-access__session-row {
    display: flex;
    gap: 0.6rem;
  }
  .settings-access__error {
    color: var(--status-warning);
    margin: 0;
  }
  .settings-access__success {
    color: var(--status-good);
    margin: 0;
  }

</style>
