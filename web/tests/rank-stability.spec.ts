import { test, expect } from '@playwright/test';

// Regression coverage for Scott's third report on this same bug: list
// reorders (Top Consumers module, Metrics page list, events) still hard-
// swapping despite animate:flip and a grace-period churn fix, both
// verified working in fake mode -- because fake mode never had the real
// box's own dozen-plus near-tied containers to churn against. fake.go's
// "tie cluster" (cadvisor, node-exporter, promtail, autoheal, speedtest-
// tracker, uptime-kuma, dozzle, flaresolverr, ntfy, syncthing,
// filebrowser, changedetection -- all under ~0.15% host CPU, dominated by
// Tick's own per-tick noise) reproduces that shape locally: real,
// independent per-tick jitter among a dozen containers, not a display
// artifact. lib/rankStability.ts's own stability layer (rolling average +
// hysteresis + a re-sort cooldown) is exact-tested in isolation
// (rankStability.test.ts); this is the integration guard that the real
// wiring (Overview.svelte -> stableTopN -> TopBarList) never lets that
// churn balloon the displayed row count or duplicate an entity -- the
// specific, confirmed-live failure mode of the OLD grace-period
// bookkeeping (opacity:0 rows piling up past its own 2x-limit bound).
test('overview: the Top Consumers module never exceeds its own limit or shows a duplicate entity, even with a dozen near-tied containers', async ({
  page,
}) => {
  test.setTimeout(30_000);
  await page.goto('#/');

  const rows = page.locator('.overview__top .top-bar-list li');
  await expect(rows.first()).toBeVisible();

  // Sampled repeatedly across real ticks (2s server cadence) rather than
  // once -- the regression is specifically an OVER-TIME growth a single
  // instant check would miss entirely.
  let maxCount = 0;
  const deadline = Date.now() + 16_000;
  while (Date.now() < deadline) {
    const names = await rows.locator('.top-bar-list__name-text').allTextContents();
    maxCount = Math.max(maxCount, names.length);
    expect(names.length, `row count ${names.length} exceeds the module's own limit`).toBeLessThanOrEqual(5);
    expect(new Set(names).size, `duplicate entity in ${JSON.stringify(names)}`).toBe(names.length);
    await page.waitForTimeout(1000);
  }
  expect(maxCount).toBeGreaterThan(0);
});

// Same invariant, the Metrics page's own hero-chart selection (a
// completely different rendering path -- TimeChart lines, no TopBarList
// in sight -- see rankStability.ts's own doc for why it shares the exact
// same stableTopN call): its own heroSlots reset a line's whole history
// the instant its assigned entity changes, so churn here is an even
// worse symptom (a chart line blanking back to empty) than the
// leaderboard's own hard-swap -- capped at MAX_HERO_LINES (10) rather
// than growing with the tie cluster's own dozen candidates.
test('metrics: the hero chart legend never exceeds 10 container chips plus the host-total chip, even with a dozen near-tied containers', async ({
  page,
}) => {
  test.setTimeout(30_000);
  await page.goto('#/top/cpu');

  const chips = page.locator('.top-consumers__chip');
  await expect.poll(() => chips.count()).toBeGreaterThan(1);

  let maxCount = 0;
  const deadline = Date.now() + 16_000;
  while (Date.now() < deadline) {
    const count = await chips.count();
    maxCount = Math.max(maxCount, count);
    expect(count).toBeLessThanOrEqual(11); // top 10 containers + host total
    await page.waitForTimeout(1000);
  }
  expect(maxCount).toBeGreaterThan(0);
});

// Settings' own "Animations" toggle (Scott's ask: force glides on/off
// regardless of the OS's own prefers-reduced-motion setting, in case
// that's part of why a real list reorder read as a hard swap for him --
// motion.svelte.ts). Same shape as the existing "theme toggle" coverage
// just above this file's own sibling in smoke.spec.ts: persistence and
// the active-button state, not the downstream animation timing itself
// (exact-tested directly in motion.test.ts's resolveReducedMotion).
test('settings: the Animations toggle persists across reload', async ({ page }) => {
  await page.goto('#/settings');

  const group = page.getByRole('group', { name: 'Animations' });
  await expect(group).toBeVisible();

  // Fresh context, no stored preference yet -> System is the default.
  await expect(group.getByRole('button', { name: 'System', exact: true })).toHaveClass(/segmented__btn--active/);

  await group.getByRole('button', { name: 'On', exact: true }).click();
  await expect(group.getByRole('button', { name: 'On', exact: true })).toHaveClass(/segmented__btn--active/);
  await page.reload();
  await expect(
    page.getByRole('group', { name: 'Animations' }).getByRole('button', { name: 'On', exact: true }),
  ).toHaveClass(/segmented__btn--active/);

  await page.getByRole('group', { name: 'Animations' }).getByRole('button', { name: 'Off', exact: true }).click();
  await page.reload();
  await expect(
    page.getByRole('group', { name: 'Animations' }).getByRole('button', { name: 'Off', exact: true }),
  ).toHaveClass(/segmented__btn--active/);

  // Independent of the Theme toggle right next to it in the same card.
  await expect(page.getByRole('group', { name: 'Theme' }).getByRole('button', { name: 'System', exact: true })).toHaveClass(
    /segmented__btn--active/,
  );
});
