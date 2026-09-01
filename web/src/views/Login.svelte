<!--
  Login: the gate screen App renders instead of the Layout whenever
  auth.needsLogin. Deliberately calm -- the crane mark, one password
  field, one button, one status line. The current hash is preserved
  through login, so a deep link opened while logged out lands where it
  pointed. Wrong password and the brute-force limiter each get their
  own line (lib/auth.ts's loginErrorMessage); the field keeps focus so
  a typo costs one keystroke, not a click.
-->
<script>
  import { auth } from '../lib/auth.svelte';
  import { loginErrorMessage } from '../lib/auth';

  let password = $state('');
  let submitting = $state(false);
  let error = $state(null);
  let field = $state(null);

  async function submit(e) {
    e.preventDefault();
    if (submitting || password === '') return;
    submitting = true;
    error = null;
    try {
      await auth.login(password);
      password = '';
    } catch (err) {
      error = loginErrorMessage(err);
      password = '';
      field?.focus();
    } finally {
      submitting = false;
    }
  }
</script>

<main class="login">
  <form class="login__card" onsubmit={submit}>
    <div class="login__brand">
      <span class="login__mark" aria-hidden="true"><span></span><span></span><span></span></span>
      <div class="login__brand-copy">
        <strong>Gantry</strong>
        <small>Server observability</small>
      </div>
    </div>

    <label class="login__field">
      <span class="microlabel">Password</span>
      <!-- svelte-ignore a11y_autofocus -- this page IS the password
           prompt; focusing anything else first would be hostile. -->
      <input
        type="password"
        bind:value={password}
        bind:this={field}
        autofocus
        autocomplete="current-password"
        disabled={submitting}
        aria-invalid={error != null}
        aria-describedby={error ? 'login-error' : undefined}
      />
    </label>

    {#if error}
      <p class="login__error" id="login-error" role="alert">{error}</p>
    {/if}

    <button type="submit" class="login__submit" disabled={submitting || password === ''}>
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
    width: 36px;
    height: 36px;
    border-radius: 10px;
    display: flex;
    align-items: flex-end;
    justify-content: center;
    gap: 3px;
    padding: 8px;
    background: linear-gradient(145deg, #a6b2ff, #5269e8);
    box-shadow: 0 8px 24px rgb(82 105 232 / 0.26);
    flex: none;
  }
  .login__mark span {
    width: 4px;
    border-radius: 3px;
    background: #141722;
  }
  .login__mark span:nth-child(1) { height: 10px; opacity: 0.65; }
  .login__mark span:nth-child(2) { height: 18px; }
  .login__mark span:nth-child(3) { height: 14px; opacity: 0.82; }
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
