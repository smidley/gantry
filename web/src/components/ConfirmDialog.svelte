<!--
  ConfirmDialog: the shared confirm-before-delete modal for Maintenance's
  Images/Containers cards (both remove-selected and prune actions, on
  both resources). Every destructive action on that page routes through
  one of these -- states exactly what's about to happen (title +
  description, e.g. counts/bytes) and exactly what it applies to (the
  scrollable `items` list), and only ever acts on a real click of its own
  confirm button -- Escape, a backdrop click, or Cancel all back out with
  no side effect. Kept generic (no images/containers-specific field
  names) since it renders whatever `items`/copy the caller hands it,
  matching Maintenance.svelte's own doc: the danger lives here, in the
  dialog, not in the page around it.

  autofocus lands on Cancel, not Confirm, on open -- a stray Enter
  keypress (this dialog appearing while the user's hand is still on the
  keyboard from whatever triggered it) must never itself confirm a
  delete.
-->
<script>
  import { onMount } from 'svelte';

  let {
    title,
    description = '',
    caveat = '',
    items = [],
    confirmLabel,
    pending = false,
    error = null,
    onConfirm,
    onCancel,
  } = $props();

  let cancelBtn = $state();
  onMount(() => {
    cancelBtn?.focus();
  });

  function handleKeydown(e) {
    if (e.key === 'Escape' && !pending) onCancel();
  }

  function handleOverlayClick(e) {
    if (e.target === e.currentTarget && !pending) onCancel();
  }
</script>

<svelte:window onkeydown={handleKeydown} />

<div class="confirm-dialog__overlay" onclick={handleOverlayClick} role="presentation">
  <div class="confirm-dialog card" role="alertdialog" aria-modal="true" aria-labelledby="confirm-dialog-title">
    <h2 id="confirm-dialog-title" class="confirm-dialog__title">{title}</h2>
    {#if description}<p class="confirm-dialog__description">{description}</p>{/if}
    {#if caveat}<p class="confirm-dialog__caveat">{caveat}</p>{/if}

    {#if items.length > 0}
      <ul class="confirm-dialog__items">
        {#each items as item (item.id)}
          <li>
            <div class="confirm-dialog__item-text">
              <span class="confirm-dialog__item-primary">{item.primary}</span>
              {#if item.secondary}<span class="confirm-dialog__item-secondary">{item.secondary}</span>{/if}
            </div>
            {#if item.warning}<span class="confirm-dialog__item-warning">{item.warning}</span>{/if}
          </li>
        {/each}
      </ul>
    {/if}

    {#if error}<p class="confirm-dialog__error">{error}</p>{/if}

    <div class="confirm-dialog__actions">
      <button type="button" class="confirm-dialog__cancel" bind:this={cancelBtn} onclick={onCancel} disabled={pending}>
        Cancel
      </button>
      <button type="button" class="confirm-dialog__confirm" onclick={onConfirm} disabled={pending}>
        {pending ? 'Working…' : confirmLabel}
      </button>
    </div>
  </div>
</div>

<style>
  .confirm-dialog__overlay {
    position: fixed;
    inset: 0;
    z-index: 20;
    background: color-mix(in oklab, black 45%, transparent);
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 1rem;
  }
  .confirm-dialog {
    width: 100%;
    max-width: 28rem;
    max-height: calc(100vh - 2rem);
    padding: 1.25rem;
    display: flex;
    flex-direction: column;
    gap: 0.65rem;
    overflow: hidden;
  }
  .confirm-dialog__title {
    margin: 0;
    font-family: var(--font-display);
    font-weight: 700;
    font-size: 1.1rem;
    color: var(--ink);
  }
  .confirm-dialog__description {
    margin: 0;
    font-size: 0.9rem;
    color: var(--ink);
  }
  .confirm-dialog__caveat {
    margin: 0;
    font-size: 0.8rem;
    color: var(--ink-2);
  }
  .confirm-dialog__items {
    margin: 0;
    padding: 0.5rem;
    list-style: none;
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
    overflow-y: auto;
    max-height: 14rem;
    border: 1px solid color-mix(in oklab, var(--ink) 10%, transparent);
    border-radius: 6px;
    background: color-mix(in oklab, var(--ink) 3%, transparent);
  }
  .confirm-dialog__items li {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 0.5rem;
    padding: 0.25rem 0.4rem;
  }
  .confirm-dialog__item-text {
    display: flex;
    flex-direction: column;
    min-width: 0;
  }
  .confirm-dialog__item-primary {
    font-size: 0.85rem;
    color: var(--ink);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .confirm-dialog__item-secondary {
    font-family: var(--font-mono);
    font-size: 0.72rem;
    color: var(--ink-2);
  }
  .confirm-dialog__item-warning {
    flex-shrink: 0;
    font-family: var(--font-mono);
    font-size: 0.68rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    padding: 0.15rem 0.45rem;
    border-radius: 999px;
    background: color-mix(in oklab, var(--status-warning) 18%, transparent);
    color: var(--status-warning);
    white-space: nowrap;
  }
  .confirm-dialog__error {
    margin: 0;
    font-size: 0.82rem;
    color: var(--status-critical);
  }
  .confirm-dialog__actions {
    display: flex;
    justify-content: flex-end;
    gap: 0.6rem;
    margin-top: 0.25rem;
  }
  .confirm-dialog__cancel,
  .confirm-dialog__confirm {
    min-height: 40px;
    padding: 0 1.1rem;
    border-radius: 6px;
    font-size: 0.85rem;
    cursor: pointer;
  }
  .confirm-dialog__cancel {
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: transparent;
    color: var(--ink);
  }
  .confirm-dialog__cancel:hover {
    background: color-mix(in oklab, var(--ink) 6%, transparent);
  }
  /* The one deliberately loud control on this whole page (Maintenance's
     own doc: "the danger lives in the confirm dialog") -- a solid fill,
     not the tinted-pill treatment every other action button in this app
     uses, so the single actually-irreversible click reads as heavier
     than an ordinary confirm. */
  .confirm-dialog__confirm {
    border: 1px solid color-mix(in oklab, var(--status-critical) 60%, transparent);
    background: var(--status-critical);
    color: white;
    font-weight: 600;
  }
  .confirm-dialog__confirm:hover {
    background: color-mix(in oklab, var(--status-critical) 85%, black);
  }
  .confirm-dialog__cancel:disabled,
  .confirm-dialog__confirm:disabled {
    opacity: 0.6;
    cursor: default;
  }
</style>
