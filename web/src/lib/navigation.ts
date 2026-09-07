let pendingPosition = 0;
let restorePosition = false;
let navigating = false;

// Cache positions before a hash transition can collapse the old document.
export function installNavigation() {
  const positions = new Map<string, number>();
  let current = location.hash;
  let clicked = false;
  let traversing = false;
  const previousRestoration = history.scrollRestoration;
  history.scrollRestoration = 'manual';
  const onClick = (event: MouseEvent) => {
    const anchor = event.target instanceof Element ? event.target.closest('a[href^="#/"]') : null;
    if (anchor && !event.metaKey && !event.ctrlKey && !event.shiftKey && event.button === 0) {
      positions.set(current, window.scrollY);
      clicked = true;
    }
  };
  const onPop = () => { traversing = !clicked; };
  const onScroll = () => {
    if (!navigating && current === location.hash) positions.set(current, window.scrollY);
  };
  const onHash = () => {
    navigating = true;
    current = location.hash;
    restorePosition = traversing;
    pendingPosition = traversing ? positions.get(current) ?? 0 : 0;
    clicked = false;
    traversing = false;
  };
  document.addEventListener('click', onClick, true);
  window.addEventListener('popstate', onPop);
  window.addEventListener('hashchange', onHash);
  window.addEventListener('scroll', onScroll, { passive: true });
  return () => {
    history.scrollRestoration = previousRestoration;
    document.removeEventListener('click', onClick, true);
    window.removeEventListener('popstate', onPop);
    window.removeEventListener('hashchange', onHash);
    window.removeEventListener('scroll', onScroll);
  };
}

export function routeContent(node: HTMLElement, _route?: unknown) {
  let frame = 0;
  let headingFocused = false;
  let target = 0;
  let hash = location.hash;
  let restoring = true;
  function stopRestoring() {
    restoring = false;
    navigating = false;
    cancelAnimationFrame(frame);
    resize.disconnect();
  }
  function place() {
    cancelAnimationFrame(frame);
    frame = requestAnimationFrame(() => {
      if (!node.isConnected || location.hash !== hash) return;
      const heading = node.querySelector<HTMLElement>('h1');
      if (!heading) return;
      document.title = `${heading.textContent?.trim() || 'Dashboard'} · Gantry`;
      heading.tabIndex = -1;
      if (!headingFocused) { heading.focus({ preventScroll: true }); headingFocused = true; }
      if (!restoring) return;
      window.scrollTo({ top: target, behavior: 'instant' });
      // Settings and other async views can initially be shorter than the
      // saved position. Restore again when their content has enough height.
      if (document.documentElement.scrollHeight - innerHeight >= target) {
        stopRestoring();
      }
    });
  }
  const resize = new ResizeObserver(place);
  const mutations = new MutationObserver(() => { if (!headingFocused) place(); });
  mutations.observe(node, { childList: true, subtree: true });
  const userIntent = () => stopRestoring();
  window.addEventListener('wheel', userIntent, { passive: true });
  window.addEventListener('touchstart', userIntent, { passive: true });
  window.addEventListener('keydown', userIntent);
  function begin() {
    hash = location.hash;
    target = restorePosition ? pendingPosition : 0;
    headingFocused = false;
    restoring = true;
    resize.observe(node);
    place();
  }
  begin();
  return {
    update: begin,
    destroy() {
      mutations.disconnect(); resize.disconnect(); cancelAnimationFrame(frame);
      window.removeEventListener('wheel', userIntent);
      window.removeEventListener('touchstart', userIntent);
      window.removeEventListener('keydown', userIntent);
    },
  };
}
