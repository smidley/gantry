// A shared Svelte action for modal focus containment and restoration.
// Inert each sibling along the ancestor chain, including portaled shells.
export function modal(node: HTMLElement) {
  const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  const changed: Array<[HTMLElement, boolean]> = [];
  let branch: HTMLElement | null = node;
  while (branch?.parentElement) {
    for (const sibling of branch.parentElement.children) {
      if (sibling !== branch && sibling instanceof HTMLElement) {
        changed.push([sibling, sibling.inert]);
        sibling.inert = true;
      }
    }
    branch = branch.parentElement;
  }
  const previousOverflow = document.body.style.overflow;
  document.body.style.overflow = 'hidden';
  const candidates = () => Array.from(node.querySelectorAll<HTMLElement>(
    'button:not([disabled]), a[href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
  )).filter((el) => !el.hidden && el.getClientRects().length > 0);
  node.tabIndex = -1;
  function keydown(event: KeyboardEvent) {
    if (event.key !== 'Tab') return;
    const items = candidates();
    const first = items[0] ?? node;
    const last = items.at(-1) ?? node;
    if (event.shiftKey && (document.activeElement === first || !node.contains(document.activeElement))) {
      event.preventDefault(); last.focus();
    } else if (!event.shiftKey && (document.activeElement === last || !node.contains(document.activeElement))) {
      event.preventDefault(); first.focus();
    }
  }
  node.addEventListener('keydown', keydown);
  return {
    destroy() {
      node.removeEventListener('keydown', keydown);
      for (const [element, inert] of changed) element.inert = inert;
      document.body.style.overflow = previousOverflow;
      if (previous?.isConnected) previous.focus({ preventScroll: true });
    },
  };
}
