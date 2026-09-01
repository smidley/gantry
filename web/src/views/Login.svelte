<!--
  Login: the gate screen App renders instead of the Layout whenever
  auth.needsLogin. Deliberately calm -- the crane mark, a username field,
  a password field, one button, one status line. The current hash is
  preserved through login, so a deep link opened while logged out lands
  where it pointed. Wrong credentials and the brute-force limiter each
  get their own line (lib/auth.ts's loginErrorMessage); on failure focus
  returns to the password field so a typo costs one keystroke, not a
  click.
-->
<script>
  import { auth } from '../lib/auth.svelte';
  import { loginErrorMessage } from '../lib/auth';

  let username = $state('');
  let password = $state('');
  let submitting = $state(false);
  let error = $state(null);
  let passwordField = $state(null);

  async function submit(e) {
    e.preventDefault();
    if (submitting || username.trim() === '' || password === '') return;
    submitting = true;
    error = null;
    try {
      await auth.login(username, password);
      password = '';
    } catch (err) {
      error = loginErrorMessage(err);
      password = '';
      passwordField?.focus();
    } finally {
      submitting = false;
    }
  }
</script>

<main class="login">
  <form class="login__card" onsubmit={submit}>
    <div class="login__brand">
      <span class="login__mark" aria-hidden="true">
        <!-- The app icon (assets/icon/gantry.svg), inlined so login shows
             the real crane; geometry stays in lockstep with Sidebar's copy
             and the master -- edit all or none. -->
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
      <div class="login__brand-copy">
        <strong>Gantry</strong>
        <small>Container observability</small>
      </div>
    </div>

    <label class="login__field">
      <span class="microlabel">Username</span>
      <!-- svelte-ignore a11y_autofocus -- this page IS the login prompt;
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
        aria-describedby={error ? 'login-error' : undefined}
      />
    </label>

    <label class="login__field">
      <span class="microlabel">Password</span>
      <input
        type="password"
        bind:value={password}
        bind:this={passwordField}
        autocomplete="current-password"
        disabled={submitting}
        aria-invalid={error != null}
        aria-describedby={error ? 'login-error' : undefined}
      />
    </label>

    {#if error}
      <p class="login__error" id="login-error" role="alert">{error}</p>
    {/if}

    <button type="submit" class="login__submit" disabled={submitting || username.trim() === '' || password === ''}>
      {submitting ? 'Unlocking…' : 'Unlock'}
    </button>
  </form>
</main>

<style>
  .login {
    min-height: 100vh;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 1.5rem;
    background: var(--page);
  }
  .login__card {
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
  .login__brand {
    display: flex;
    align-items: center;
    gap: 0.75rem;
  }
  /* The Sidebar's crane mark, verbatim proportions -- one identity,
     both sides of the gate. */
  .login__mark {
    width: 56px;
    height: 56px;
    flex: none;
    box-shadow: 0 8px 24px rgb(11 11 11 / 0.28);
    border-radius: 13px;
  }
  .login__mark svg {
    display: block;
    width: 100%;
    height: 100%;
    border-radius: 13px;
  }
  .login__brand-copy {
    display: flex;
    flex-direction: column;
  }
  .login__brand-copy strong {
    font-size: 1rem;
    letter-spacing: 0.01em;
  }
  .login__brand-copy small {
    color: var(--ink-2);
    font-size: 0.75rem;
  }
  .login__field {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
  }
  .login__field input {
    min-height: 42px;
    padding: 0 0.75rem;
    border-radius: 8px;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: var(--surface);
    color: var(--ink);
    font-size: 0.95rem;
  }
  .login__field input:focus-visible {
    outline: 2px solid var(--series-1);
    outline-offset: 1px;
  }
  .login__error {
    margin: 0;
    font-size: 0.82rem;
    color: var(--status-warning);
  }
  .login__submit {
    min-height: 42px;
    border-radius: 8px;
    border: 1px solid var(--series-1);
    background: color-mix(in oklab, var(--series-1) 15%, transparent);
    color: var(--series-1);
    font-size: 0.9rem;
    font-weight: 500;
    cursor: pointer;
  }
  .login__submit:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }
</style>
