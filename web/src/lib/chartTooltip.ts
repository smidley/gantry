export function chartTooltip(node: HTMLElement, position: { x: number; y: number }) {
  let frame = 0;
  function place() {
    const parent = node.offsetParent?.getBoundingClientRect();
    if (!parent) return;
    const width = node.offsetWidth;
    const height = node.offsetHeight;
    const x = Math.max(8 - parent.left, Math.min(position.x + 12, innerWidth - parent.left - width - 8));
    const y = Math.max(8 - parent.top, Math.min(position.y + 12, innerHeight - parent.top - height - 8));
    node.style.left = `${x}px`;
    node.style.top = `${y}px`;
  }
  frame = requestAnimationFrame(place);
  return {
    update(next: { x: number; y: number }) { position = next; place(); },
    destroy() { cancelAnimationFrame(frame); },
  };
}
