<!--
  Setup: the first-run screen App renders instead of the Layout whenever
  auth.needsSetup -- a box with no credential yet. Same calm frame as the
  login gate (the crane mark, quiet card), but it CREATES the single
  account: username, password, confirm. Validation is local first
  (lib/auth.ts's credentialFormError) so a mismatch or short password
  answers with no round-trip; on success the server hands back a session
  and auth.setup flips straight to the app -- no separate login step.
-->
<script>
  import { auth } from '../lib/auth.svelte';
  import { credentialFormError, loginErrorMessage } from '../lib/auth';

  let username = $state('');
  let password = $state('');
  let confirm = $state('');
  let submitting = $state(false);
  let error = $state(null);

  async function submit(e) {
    e.preventDefault();
    if (submitting) return;
    const problem = credentialFormError({ username, password, confirm, passwordRequired: true });
    if (problem) {
      error = problem;
      return;
    }
    submitting = true;
    error = null;
    try {
      await auth.setup(username, password);
      password = '';
      confirm = '';
    } catch (err) {
      error = loginErrorMessage(err);
    } finally {
      submitting = false;
    }
  }
</script>

<main class="setup">
  <form class="setup__card" onsubmit={submit} novalidate>
    <div class="setup__brand">
      <span class="setup__mark" aria-hidden="true">
        <!-- The app icon (assets/icon/gantry.svg), inlined so setup shows
             the real crane; geometry stays in lockstep with the login
             screen, the Sidebar copy, and the master -- edit all or none. -->
        <svg viewBox="0 0 256 256" xmlns="http://www.w3.org/2000/svg">
          <rect x="0" y="0" width="256" height="256" rx="56" fill="#0b0b0b" />
          <rect x="0" y="0" width="256" height="256" rx="56" fill="none" stroke="#ffffff" stroke-opacity="0.08" stroke-width="3" />
          <rect x="54" y="82" width="156" height="26" rx="4" fill="#2a78d6" />
          <rect x="54" y="108" width="28" height="83" rx="4" fill="#2a78d6" />
          <rect x="146" y="108" width="28" height="83" rx="4" fill="#2a78d6" />
          <rect x="105" y="106" width="21" height="15" rx="2" fill="#ffffff" />
          <rect x="111" y="119" width="9" height="25" fill="#ffffff" />
          <rect x="85" y="144" width="61" height="40" rx="4" fill="#ffffff" />
          <rect x="107" y="144" width="5" height="40" fill="#2a78d6" />
          <rect x="119" y="144" width="5" height="40" fill="#2a78d6" />
        </svg>
      </span>
      <div class="setup__brand-copy">
        <strong>Create your Gantry login</strong>
        <small>Set a username and password. You'll use them every time you open this dashboard.</small>
      </div>
    </div>

    <label class="setup__field">
      <span class="microlabel">Username</span>
      <!-- svelte-ignore a11y_autofocus -- this page IS the setup prompt;
           focusing the first field first is the least surprising thing. -->
      <input
        type="text"
        bind:value={username}
        autofocus
        autocomplete="username"
        autocapitalize="none"
        autocorrect="off"
        spellcheck="false"
        disabled={submitting}
        aria-invalid={error != null}
        aria-describedby={error ? 'setup-error' : undefined}
      />
    </label>

    <label class="setup__field">
      <span class="microlabel">Password</span>
      <input
        type="password"
        bind:value={password}
        autocomplete="new-password"
        disabled={submitting}
        aria-invalid={error != null}
        aria-describedby={error ? 'setup-error' : undefined}
      />
    </label>

    <label class="setup__field">
      <span class="microlabel">Confirm password</span>
      <input
        type="password"
        bind:value={confirm}
        autocomplete="new-password"
        disabled={submitting}
        aria-invalid={error != null}
        aria-describedby={error ? 'setup-error' : undefined}
      />
    </label>

    <p class="setup__hint">At least 8 characters.</p>

    {#if error}
      <p class="setup__error" id="setup-error" role="alert">{error}</p>
    {/if}

    <button type="submit" class="setup__submit" disabled={submitting}>
      {submitting ? 'Creating…' : 'Create login'}
    </button>
  </form>
</main>

<style>
  .setup {
    min-height: 100vh;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 1.5rem;
    background: var(--page);
  }
  .setup__card {
    width: 100%;
    max-width: 22rem;
    display: flex;
    flex-direction: column;
    gap: 1rem;
    padding: 1.75rem;
    border-radius: 14px;
    background: var(--surface);
    border: 1px solid color-mix(in oklab, var(--ink) 10%, transparent);
    box-shadow: 0 18px 48px rgb(10 14 30 / 0.12);
  }
  .setup__brand {
    display: flex;
    align-items: center;
    gap: 0.75rem;
  }
  /* The Sidebar's crane mark, verbatim proportions -- one identity
     across setup, login, and the app. */
  .setup__mark {
    width: 56px;
    height: 56px;
    flex: none;
    box-shadow: 0 8px 24px rgb(11 11 11 / 0.28);
    border-radius: 13px;
  }
  .setup__mark svg {
    display: block;
    width: 100%;
    height: 100%;
    border-radius: 13px;
  }
  .setup__brand-copy {
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
  }
  .setup__brand-copy strong {
    font-size: 1rem;
    letter-spacing: 0.01em;
  }
  .setup__brand-copy small {
    color: var(--ink-2);
    font-size: 0.75rem;
    line-height: 1.35;
  }
  .setup__field {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
  }
  .setup__field input {
    min-height: 42px;
    padding: 0 0.75rem;
    border-radius: 8px;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: var(--surface);
    color: var(--ink);
    font-size: 0.95rem;
  }
  .setup__field input:focus-visible {
    outline: 2px solid var(--series-1);
    outline-offset: 1px;
  }
  .setup__hint {
    margin: -0.35rem 0 0;
    font-size: 0.75rem;
    color: var(--ink-2);
  }
  .setup__error {
    margin: 0;
    font-size: 0.82rem;
    color: var(--status-warning);
  }
  .setup__submit {
    min-height: 42px;
    border-radius: 8px;
    border: 1px solid var(--series-1);
    background: color-mix(in oklab, var(--series-1) 15%, transparent);
    color: var(--series-1);
    font-size: 0.9rem;
    font-weight: 500;
    cursor: pointer;
  }
  .setup__submit:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }
</style>
