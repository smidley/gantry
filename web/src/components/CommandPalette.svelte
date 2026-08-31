<script>
  import { onMount, tick } from 'svelte';
  import { routes } from '../lib/router';
  import { live } from '../lib/sse.svelte';
  import { theme } from '../lib/theme.svelte';

  let open = $state(false);
  let query = $state('');
  let activeIndex = $state(0);
  let inputEl = $state();

  let items = $derived.by(() => {
    const navigation = routes.map((item) => ({
      id: `route:${item.name}`,
      label: item.label,
      detail: 'Page',
      href: item.hash,
      keywords: `${item.label} page navigate`,
    }));
    const containers = Object.entries(live.frame?.containers ?? {})
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([name, container]) => ({
        id: `container:${name}`,
        label: name,
        detail: `Container · ${container.state}`,
        href: `#/containers/${encodeURIComponent(name)}`,
        keywords: `${name} ${container.image ?? ''} container ${container.state}`,
      }));
    const disks = Object.keys(live.frame?.disks ?? {})
      .sort()
      .map((name) => ({
        id: `disk:${name}`,
        label: name,
        detail: `Storage device${live.frame?.disk_meta?.[name]?.kind ? ` · ${live.frame.disk_meta[name].kind.toUpperCase()}` : ''}`,
        href: '#/storage',
        keywords: `${name} disk drive storage ${live.frame?.disk_meta?.[name]?.device ?? ''}`,
      }));
    const gpus = Object.keys(live.frame?.gpu ?? {})
      .sort()
      .map((name) => ({
        id: `gpu:${name}`,
        label: live.frame?.gpu_meta?.[name]?.vendor ? `${live.frame.gpu_meta[name].vendor} GPU` : name,
        detail: `GPU · ${name}`,
        href: '#/gpu',
        keywords: `${name} gpu graphics ${live.frame?.gpu_meta?.[name]?.vendor ?? ''}`,
      }));
    const actions = [
      {
        id: 'action:theme',
        label: 'Cycle appearance theme',
        detail: `Action · currently ${theme.preference}`,
        keywords: 'theme appearance light dark system',
        run: () => theme.cycle(),
      },
    ];
    return [...navigation, ...containers, ...disks, ...gpus, ...actions];
  });

  let filteredItems = $derived.by(() => {
    const terms = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
    if (terms.length === 0) return items.slice(0, 12);
    return items
      .filter((item) => terms.every((term) => `${item.label} ${item.detail} ${item.keywords}`.toLowerCase().includes(term)))
      .slice(0, 20);
  });

  $effect(() => {
    query;
    if (activeIndex >= filteredItems.length) activeIndex = Math.max(0, filteredItems.length - 1);
  });

  async function show() {
    open = true;
    query = '';
    activeIndex = 0;
    await tick();
    inputEl?.focus();
  }

  function close() {
    open = false;
  }

  function choose(item) {
    if (item.run) item.run();
    if (item.href && typeof window !== 'undefined') window.location.hash = item.href;
    close();
  }

  function handleInputKeydown(event) {
    if (event.key === 'ArrowDown') {
      event.preventDefault();
      activeIndex = Math.min(filteredItems.length - 1, activeIndex + 1);
    } else if (event.key === 'ArrowUp') {
      event.preventDefault();
      activeIndex = Math.max(0, activeIndex - 1);
    } else if (event.key === 'Enter' && filteredItems[activeIndex]) {
      event.preventDefault();
      choose(filteredItems[activeIndex]);
    } else if (event.key === 'Escape') {
      event.preventDefault();
      close();
    }
  }

  onMount(() => {
    const onKeydown = (event) => {
      const target = event.target;
      const typing = target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement || target?.isContentEditable;
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault();
        open ? close() : show();
      } else if (!typing && event.key === '/') {
        event.preventDefault();
        show();
      } else if (open && event.key === 'Escape') {
        close();
      }
    };
    window.addEventListener('keydown', onKeydown);
    return () => window.removeEventListener('keydown', onKeydown);
  });
</script>

<button type="button" class="command-palette__trigger" onclick={show} aria-label="Search and jump to anything">
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
    <circle cx="11" cy="11" r="6.5"></circle><path d="m16 16 4 4"></path>
  </svg>
  <span>Search</span>
  <kbd>⌘K</kbd>
</button>

{#if open}
  <div class="command-palette" role="presentation">
    <button type="button" class="command-palette__backdrop" onclick={close} aria-label="Close search"></button>
    <div class="command-palette__panel" role="dialog" aria-modal="true" aria-label="Search Gantry">
      <div class="command-palette__input-wrap">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
          <circle cx="11" cy="11" r="6.5"></circle><path d="m16 16 4 4"></path>
        </svg>
        <input
          bind:this={inputEl}
          bind:value={query}
          onkeydown={handleInputKeydown}
          placeholder="Find a page, container, disk, GPU, or action…"
          aria-label="Search Gantry"
          aria-activedescendant={filteredItems[activeIndex] ? `command-${activeIndex}` : undefined}
        />
        <kbd>Esc</kbd>
      </div>
      <div class="command-palette__results" role="listbox" aria-label="Search results">
        {#if filteredItems.length === 0}
          <p class="command-palette__empty">No matching destinations.</p>
        {:else}
          {#each filteredItems as item, i (item.id)}
            <button
              id={`command-${i}`}
              type="button"
              role="option"
              aria-selected={i === activeIndex}
              class="command-palette__result"
              class:command-palette__result--active={i === activeIndex}
              onmouseenter={() => (activeIndex = i)}
              onclick={() => choose(item)}
            >
              <span class="command-palette__result-mark" aria-hidden="true"></span>
              <span class="command-palette__result-copy">
                <strong>{item.label}</strong>
                <small>{item.detail}</small>
              </span>
              <span class="command-palette__enter" aria-hidden="true">↵</span>
            </button>
          {/each}
        {/if}
      </div>
      <footer class="command-palette__footer">
        <span><kbd>↑</kbd><kbd>↓</kbd> Navigate</span><span><kbd>↵</kbd> Open</span>
      </footer>
    </div>
  </div>
{/if}

<style>
  .command-palette__trigger {
    min-height: 36px;
    display: inline-flex;
    align-items: center;
    gap: 0.45rem;
    padding: 0 0.6rem;
    border: 1px solid var(--border);
    border-radius: 9px;
    background: transparent;
    color: var(--ink-2);
    cursor: pointer;
  }
  .command-palette__trigger:hover {
    border-color: var(--border-strong);
    background: var(--surface-soft);
    color: var(--ink);
  }
  .command-palette__trigger svg,
  .command-palette__input-wrap svg {
    width: 15px;
    height: 15px;
    color: var(--accent);
  }
  .command-palette__trigger > span {
    font-size: 0.72rem;
    font-weight: 550;
  }
  kbd {
    padding: 0.12rem 0.3rem;
    border: 1px solid var(--border);
    border-radius: 5px;
    background: var(--surface-soft);
    color: var(--ink-3);
    font-family: var(--font-mono);
    font-size: 0.6rem;
    line-height: 1.2;
  }
  .command-palette {
    position: fixed;
    inset: 0;
    z-index: 100;
    display: grid;
    place-items: start center;
    padding: min(16vh, 8rem) 1rem 1rem;
  }
  .command-palette__backdrop {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    border: 0;
    background: color-mix(in oklab, var(--ink) 36%, transparent);
    backdrop-filter: blur(8px);
    cursor: default;
  }
  .command-palette__panel {
    position: relative;
    width: min(42rem, 100%);
    max-height: min(68vh, 38rem);
    overflow: hidden;
    border: 1px solid var(--border-strong);
    border-radius: 16px;
    background: color-mix(in oklab, var(--surface) 97%, transparent);
    box-shadow: 0 28px 80px color-mix(in oklab, var(--ink) 28%, transparent);
    backdrop-filter: blur(18px) saturate(125%);
  }
  .command-palette__input-wrap {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr) auto;
    align-items: center;
    gap: 0.7rem;
    padding: 0.85rem 1rem;
    border-bottom: 1px solid var(--border);
  }
  .command-palette__input-wrap input {
    width: 100%;
    min-height: 36px;
    border: 0;
    outline: 0;
    background: transparent;
    color: var(--ink);
    font-size: 0.96rem;
  }
  .command-palette__results {
    max-height: calc(min(68vh, 38rem) - 7rem);
    overflow-y: auto;
    padding: 0.5rem;
  }
  .command-palette__result {
    width: 100%;
    display: grid;
    grid-template-columns: auto minmax(0, 1fr) auto;
    align-items: center;
    gap: 0.7rem;
    min-height: 52px;
    padding: 0.55rem 0.7rem;
    border: 0;
    border-radius: 9px;
    background: transparent;
    color: var(--ink);
    text-align: left;
    cursor: pointer;
  }
  .command-palette__result--active {
    background: color-mix(in oklab, var(--accent) 9%, var(--surface-soft));
  }
  .command-palette__result-mark {
    width: 8px;
    height: 8px;
    border-radius: 3px;
    background: var(--accent);
    box-shadow: 0 0 0 4px color-mix(in oklab, var(--accent) 10%, transparent);
  }
  .command-palette__result-copy {
    display: flex;
    flex-direction: column;
    gap: 0.12rem;
    min-width: 0;
  }
  .command-palette__result-copy strong {
    overflow: hidden;
    font-size: 0.82rem;
    font-weight: 620;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .command-palette__result-copy small {
    color: var(--ink-2);
    font-size: 0.68rem;
  }
  .command-palette__enter {
    color: var(--ink-3);
    opacity: 0;
  }
  .command-palette__result--active .command-palette__enter {
    opacity: 1;
  }
  .command-palette__empty {
    margin: 0;
    padding: 2rem 1rem;
    color: var(--ink-2);
    text-align: center;
    font-size: 0.82rem;
  }
  .command-palette__footer {
    display: flex;
    gap: 1rem;
    padding: 0.55rem 0.8rem;
    border-top: 1px solid var(--border);
    color: var(--ink-3);
    font-size: 0.64rem;
  }
  .command-palette__footer span {
    display: inline-flex;
    align-items: center;
    gap: 0.25rem;
  }
  @media (max-width: 34rem) {
    .command-palette__trigger > span,
    .command-palette__trigger > kbd {
      display: none;
    }
    .command-palette__trigger {
      min-width: 36px;
      justify-content: center;
    }
    .command-palette {
      padding-top: 4.5rem;
    }
  }
</style>
