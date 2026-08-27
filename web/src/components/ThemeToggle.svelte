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
  <span class="microlabel">{LABEL[theme.preference]}</span>
</button>

<style>
  .theme-toggle {
    min-height: 40px;
    min-width: 40px;
    padding: 0 0.75rem;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    background: transparent;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    border-radius: 6px;
    color: var(--ink);
    cursor: pointer;
  }
  .theme-toggle:hover {
    background: color-mix(in oklab, var(--ink) 6%, transparent);
  }
</style>
