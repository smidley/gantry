<!--
  ThemeToggle: cycles system -> light -> dark -> system. Shows the
  CURRENT preference (not the resolved light/dark) so "system" stays
  visibly distinct from an explicit choice.
-->
<script>
  import { theme } from '../lib/theme.svelte';

  const LABEL = { system: 'System', light: 'Light', dark: 'Dark' };
</script>

<button
  type="button"
  class="theme-toggle"
  onclick={() => theme.cycle()}
  title={`Theme: ${LABEL[theme.preference]} (click to cycle)`}
>
  <span class="theme-toggle__icon" aria-hidden="true">
    {#if theme.preference === 'light'}
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><circle cx="12" cy="12" r="3.5"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/></svg>
    {:else if theme.preference === 'dark'}
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M20 15.2A8.3 8.3 0 0 1 8.8 4a8.5 8.5 0 1 0 11.2 11.2Z"/></svg>
    {:else}
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><rect x="3" y="4" width="18" height="13" rx="2"/><path d="M8 21h8M12 17v4"/></svg>
    {/if}
  </span>
  <span class="theme-toggle__label">{LABEL[theme.preference]}</span>
</button>

<style>
  .theme-toggle {
    min-height: 36px;
    min-width: 36px;
    padding: 0 0.7rem;
    gap: 0.45rem;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    background: transparent;
    border: 1px solid var(--border);
    border-radius: 9px;
    color: var(--ink);
    cursor: pointer;
  }
  .theme-toggle:hover {
    background: var(--surface-soft);
    border-color: var(--border-strong);
  }
  .theme-toggle__icon {
    display: inline-flex;
    width: 15px;
    height: 15px;
    color: var(--accent);
  }
  .theme-toggle__icon svg {
    width: 100%;
    height: 100%;
  }
  .theme-toggle__label {
    font-family: var(--font-mono);
    font-size: 0.64rem;
    text-transform: uppercase;
    letter-spacing: 0.07em;
    color: var(--ink-2);
  }
  @media (max-width: 34rem) {
    .theme-toggle__label {
      display: none;
    }
  }
</style>
