<!--
  LogViewer: streams one container's logs (follow=1, tail=500) into a
  2000-line capped buffer. Follow (auto-scroll) and pause are separate,
  independent controls: follow only decides whether new lines pull the
  view down to the bottom; pause stops the buffer itself from accepting
  new lines at all (see the pause doc below for what that trades off).
  404/unavailable (unknown container, or fake-data mode's no-docker-
  collector case -- see api_logs.go's own doc) renders a friendly empty
  state instead of a broken pane.

  Severity filter (additive -- Scott's own ask: "make logs filterable by
  type - Info, error, warning, etc."): each line's severity is classified
  ONCE, when it's first appended (classifyLogLine, lib/logSeverity.ts),
  not re-derived on every render -- lines is append-only up to MAX_LINES
  and a line's own text never changes after the fact, so there's nothing
  to re-classify later. severityFilter and the free-text filterText
  compose (AND): a severity tab narrows which lines are eligible, the
  text box further narrows those. The live per-severity counts
  (severityCounts) are deliberately over ALL buffered lines regardless of
  the text filter, so switching severity tabs while mid-search doesn't
  show counts that jump around from what's currently typed.
-->
<script>
  import { onMount } from 'svelte';
  import { streamLogs } from '../lib/api';
  import { stripAnsi } from '../lib/ansi';
  import { classifyLogLine, SEVERITY_ORDER } from '../lib/logSeverity';

  const MAX_LINES = 2000;
  const SEVERITY_LABEL = { error: 'Error', warn: 'Warn', info: 'Info', debug: 'Debug', other: 'Other' };

  let { name } = $props();

  // lines: {text, severity}[] -- see the module doc above for why
  // severity rides alongside text instead of being recomputed later.
  let lines = $state([]);
  let follow = $state(true);
  let paused = $state(false);
  let filterText = $state('');
  let severityFilter = $state('all'); // 'all' | LogSeverity
  let error = $state(null);
  let connecting = $state(true);

  let severityCounts = $derived.by(() => {
    const counts = { all: lines.length, error: 0, warn: 0, info: 0, debug: 0, other: 0 };
    for (const line of lines) counts[line.severity]++;
    return counts;
  });

  let filteredLines = $derived.by(() => {
    let base = severityFilter === 'all' ? lines : lines.filter((line) => line.severity === severityFilter);
    const needle = filterText.trim().toLowerCase();
    if (needle) base = base.filter((line) => line.text.toLowerCase().includes(needle));
    return base;
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
    // abortController actually tears down the underlying fetch/reader on
    // cleanup, rather than merely flagging the consumer loop to stop
    // reading from it. A bare boolean flag (the previous shape here)
    // left reader.read() parked forever for a quiet container: nothing
    // told the fetch, or the server's own follow=1 goroutine behind it
    // (see api_logs.go's drain doc), to actually stop, so the connection
    // -- and everything it holds open server-side -- outlived the
    // component that opened it, for every container ever visited in a
    // session. Aborting rejects the pending read with an AbortError,
    // which unwinds the for-await loop below through streamLogs' own
    // `finally` (reader.cancel()) and out to this catch, where it's
    // recognized and treated as an intentional stop, not a real failure.
    const controller = new AbortController();

    (async () => {
      let pendingPartial = '';
      try {
        for await (const chunk of streamLogs(name, { follow: true, tail: 500, signal: controller.signal })) {
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
            lines = [...lines, ...parts.map((text) => ({ text, severity: classifyLogLine(text) }))].slice(-MAX_LINES);
          }
        }
        if (pendingPartial) {
          lines = [...lines, { text: pendingPartial, severity: classifyLogLine(pendingPartial) }].slice(-MAX_LINES);
        }
      } catch (err) {
        if (err?.name === 'AbortError') return; // unmounted, or the container name changed
        connecting = false;
        error = err instanceof Error ? err.message : String(err);
      }
    })();

    return () => {
      controller.abort();
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

  <div class="segmented" role="group" aria-label="Log severity">
    <button
      type="button"
      class="segmented__btn"
      class:segmented__btn--active={severityFilter === 'all'}
      onclick={() => (severityFilter = 'all')}
    >
      All ({severityCounts.all})
    </button>
    {#each SEVERITY_ORDER as sev (sev)}
      <button
        type="button"
        class="segmented__btn"
        class:segmented__btn--active={severityFilter === sev}
        onclick={() => (severityFilter = sev)}
      >
        {SEVERITY_LABEL[sev]} ({severityCounts[sev]})
      </button>
    {/each}
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
        <div class="log-viewer__line log-viewer__line--{line.severity}">{line.text}</div>
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
  /* Severity tint (Scott: "make logs filterable by type"): text color
     only, reusing the same status tokens HealthDot/status text already
     use elsewhere -- info/other stay plain ink (the common case, no
     tint at all so the pane doesn't turn into a rainbow), debug is
     merely de-emphasized via --ink-2, matching every other muted-text
     convention in this app. */
  .log-viewer__line--error {
    color: var(--status-critical);
  }
  .log-viewer__line--warn {
    color: var(--status-warning);
  }
  .log-viewer__line--debug {
    color: var(--ink-2);
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
