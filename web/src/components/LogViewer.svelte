<!--
  LogViewer: streams one container's logs (follow=1, tail=500) into a
  2000-line capped buffer. Follow (auto-scroll) and pause are separate,
  independent controls: follow only decides whether new lines pull the
  view down to the bottom; pause stops the buffer itself from accepting
  new lines at all (see the pause doc below for what that trades off).
  404/unavailable (unknown container, or fake-data mode's no-docker-
  collector case -- see api_logs.go's own doc) renders a friendly empty
  state instead of a broken pane.
-->
<script>
  import { onMount } from 'svelte';
  import { streamLogs } from '../lib/api';
  import { stripAnsi } from '../lib/ansi';

  const MAX_LINES = 2000;

  let { name } = $props();

  let lines = $state([]);
  let follow = $state(true);
  let paused = $state(false);
  let filterText = $state('');
  let error = $state(null);
  let connecting = $state(true);

  let filteredLines = $derived.by(() => {
    const needle = filterText.trim().toLowerCase();
    if (!needle) return lines;
    return lines.filter((line) => line.toLowerCase().includes(needle));
  });

  let scrollEl = $state();

  $effect(() => {
    // Depends on filteredLines (both its length and, via the array
    // reference, its content) -- scrolls to the bottom of whatever is
    // actually RENDERED, so a filtered view still follows correctly.
    filteredLines;
    if (follow && scrollEl) {
      scrollEl.scrollTop = scrollEl.scrollHeight;
    }
  });

  onMount(() => {
    let cancelled = false;

    (async () => {
      let pendingPartial = '';
      try {
        for await (const chunk of streamLogs(name, { follow: true, tail: 500 })) {
          if (cancelled) break;
          connecting = false;
          // paused: drop incoming lines rather than buffering them
          // invisibly -- there's no separate off-screen store here, so
          // "paused" means the view is frozen at what it already has,
          // and resuming picks back up from whatever's live at that
          // point (a small gap during the pause, not a full DVR-style
          // buffer). Simple and bounded; a real "buffer while paused"
          // would need its own unbounded-until-resumed queue.
          if (paused) continue;
          const withPending = pendingPartial + stripAnsi(chunk);
          const parts = withPending.split('\n');
          pendingPartial = parts.pop() ?? '';
          if (parts.length > 0) {
            lines = [...lines, ...parts].slice(-MAX_LINES);
          }
        }
        if (!cancelled && pendingPartial) {
          lines = [...lines, pendingPartial].slice(-MAX_LINES);
        }
      } catch (err) {
        if (!cancelled) {
          connecting = false;
          error = err instanceof Error ? err.message : String(err);
        }
      }
    })();

    return () => {
      cancelled = true;
    };
  });
</script>

<div class="log-viewer">
  <div class="log-viewer__controls">
    <button
      type="button"
      class="log-viewer__btn"
      class:log-viewer__btn--active={follow}
      onclick={() => (follow = !follow)}
    >
      {follow ? 'Following' : 'Follow'}
    </button>
    <button
      type="button"
      class="log-viewer__btn"
      class:log-viewer__btn--active={paused}
      onclick={() => (paused = !paused)}
    >
      {paused ? 'Paused' : 'Pause'}
    </button>
    <input
      type="text"
      class="log-viewer__filter"
      placeholder="Filter logs…"
      bind:value={filterText}
      aria-label="Filter log lines"
    />
    <span class="microlabel log-viewer__count">{filteredLines.length} / {lines.length} lines</span>
  </div>

  {#if error}
    <div class="log-viewer__empty">
      <p>Logs aren't available for "{name}".</p>
      <p class="log-viewer__empty-detail">{error}</p>
    </div>
  {:else if connecting}
    <div class="log-viewer__empty">
      <p>Connecting to logs…</p>
    </div>
  {:else if filteredLines.length === 0}
    <div class="log-viewer__empty">
      <p>{lines.length === 0 ? 'No log output yet.' : 'No lines match that filter.'}</p>
    </div>
  {:else}
    <div class="log-viewer__pane" bind:this={scrollEl}>
      {#each filteredLines as line, i (i)}
        <div class="log-viewer__line">{line}</div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .log-viewer {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .log-viewer__controls {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    flex-wrap: wrap;
  }
  .log-viewer__btn {
    min-height: 40px;
    padding: 0 0.75rem;
    border-radius: 6px;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: transparent;
    color: var(--ink);
    font-size: 0.8rem;
    cursor: pointer;
  }
  .log-viewer__btn--active {
    background: color-mix(in oklab, var(--series-1) 15%, transparent);
    border-color: var(--series-1);
    color: var(--series-1);
  }
  .log-viewer__filter {
    min-height: 40px;
    padding: 0 0.75rem;
    border-radius: 6px;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: var(--surface);
    color: var(--ink);
    font-size: 0.85rem;
    flex: 1;
    min-width: 10rem;
  }
  .log-viewer__count {
    white-space: nowrap;
  }
  .log-viewer__pane {
    background: color-mix(in oklab, var(--ink) 4%, var(--surface));
    border: 1px solid color-mix(in oklab, var(--ink) 10%, transparent);
    border-radius: 8px;
    padding: 0.6rem 0.75rem;
    height: 24rem;
    overflow-y: auto;
    overflow-x: auto;
    font-family: var(--font-mono);
    font-size: 0.78rem;
    line-height: 1.5;
  }
  .log-viewer__line {
    white-space: pre;
    color: var(--ink);
  }
  .log-viewer__empty {
    padding: 2rem 1rem;
    text-align: center;
    color: var(--ink-2);
    border: 1px dashed color-mix(in oklab, var(--ink) 15%, transparent);
    border-radius: 8px;
  }
  .log-viewer__empty-detail {
    font-family: var(--font-mono);
    font-size: 0.78rem;
    margin-top: 0.4rem;
  }
</style>
