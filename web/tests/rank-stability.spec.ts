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
// same stableTopN call): the chip row is capped at MAX_HERO_LINES (10)
// rather than growing with the tie cluster's own dozen candidates (a
// fading, no-longer-ranked tail line can still be DRAWN past that count
// -- see heroLines' own doc in TopConsumers.svelte -- but never gets its
// own chip).
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

// D2 chart-integrity pass: heroSlots used to be keyed by RANK POSITION,
// so tick() reset a slot's WHOLE ring the instant its ASSIGNED ENTITY
// changed -- which fired not only on a genuine membership change but
// also on a mere reorder among members who never left the top-10 at
// all. Confirmed live against the real box: several containers' history
// blanked in sync every time their relative ranks merely swapped,
// reading as "lines exist only in disconnected patches." heroLines now
// keys by container IDENTITY instead, so a reorder among OTHER members
// must never touch a tracked container's own ring. The target is
// whichever container is CURRENTLY ranked #1 at test start (read
// dynamically, not hardcoded -- fake mode's own randomness means no
// single fixed name is guaranteed to be in the top-10 at every instant
// this suite happens to run): rank #1 is the one member least likely to
// fall out of the top-10 during the observation window, so its own
// oldest-visible sample can be tracked reliably, and its rank still
// fluctuates against the mid-pack/tie-cluster churn below it -- exactly
// the condition the old bug needed. Once real, it must never regress
// back to "no data" while it's still one of the rendered lines. Mirrors
// live-seed.spec.ts's own left-edge-hover technique (TimeChart has no
// DOM "empty" marker), just sampled repeatedly instead of once.
test("metrics: a continuously-tracked container's hero-chart history survives OTHER members reordering around it", async ({
  page,
}) => {
  test.setTimeout(40_000);
  await page.goto('#/top/cpu');

  const chart = page.locator('.top-consumers__header .u-over').first();
  await expect(chart).toBeVisible();
  const chips = page.locator('.top-consumers__chip');
  await expect.poll(() => chips.count(), { timeout: 15_000 }).toBeGreaterThan(1); // >=1 container chip + host total

  // The chip's own name lives in a plain trailing <span> sibling AFTER
  // ContainerIcon (which renders its own fallback-letter text first) --
  // .last() on every descendant span reliably lands on that trailing
  // one, in DOM order, regardless of whatever ContainerIcon's own markup
  // contains. A bare .textContent() on the whole chip would instead
  // include that fallback letter too ("J jellyfin"), which the tooltip
  // rows below (plain "jellyfin 1.7%" text, no icon) would never match.
  const target = (await chips.first().locator('span').last().textContent())?.trim();
  if (!target || target === 'Host total') throw new Error(`expected a real container as the top-ranked chip, got "${target}"`);

  async function targetEdgeRow(): Promise<string | undefined> {
    await chart.hover({ position: { x: 2, y: 10 } });
    const rows = page.locator('.top-consumers__header .time-chart__tooltip-row');
    const texts = await rows.allTextContents();
    return texts.find((t) => t.includes(target));
  }

  // prevReal is undefined whenever the target isn't currently one of the
  // rendered lines at all (a real, hysteresis-gated membership change --
  // no claim either way while that's true); once it IS rendered, its
  // real/not-real edge status may only ever go from not-real to real,
  // never the other way, for as long as it stays rendered.
  let prevReal: boolean | undefined;
  let everReal = false;
  const deadline = Date.now() + 25_000;
  while (Date.now() < deadline) {
    const row = await targetEdgeRow();
    if (row === undefined) {
      prevReal = undefined;
    } else {
      const isReal = !row.includes('—');
      if (prevReal === true) {
        expect(isReal, `${target}'s oldest-visible sample regressed from real to "no data" -- saw "${row}"`).toBe(true);
      }
      everReal = everReal || isReal;
      prevReal = isReal;
    }
    await page.waitForTimeout(2_000);
  }
  expect(everReal, `${target} never showed a real value at the chart's oldest visible edge`).toBe(true);
});

// Fourth report on this same bug class: leaderboard rows left garbled/
// overlapping (frozen mid-transition), reproducible on both surfaces
// above via two DIFFERENT triggers. Overview's module hits it (rarely)
// through rankStability's own eviction+fill split; the Metrics page's
// own panel has no cap at all (COMPLETE_LIST_LIMIT=500, stableTopN never
// evicts there), so that was never its mechanism -- switching the
// resource tab instead swaps metricKey for every row's own each-block
// key AT ONCE (see TopBarList's own metricKey doc), a much bigger
// simultaneous add+remove than a single eviction. TopBarList.svelte's
// fix (animate:flip only -- no in:/out: transition on these rows at all
// any more) removes the async outro bookkeeping that could get stuck
// either way, regardless of how many rows enter/leave in the same
// reconciliation -- this pair hammers both triggers directly (sustained
// real-tick churn here, rapid resource-tab switching below) instead of
// waiting on the rare timing either one used to need to actually surface.
test('overview: leaderboard rows never get stuck mid-transition under sustained churn', async ({ page }) => {
  test.setTimeout(35_000);
  await page.goto('#/');

  const list = page.locator('.overview__top .top-bar-list');
  await expect(list.locator('li').first()).toBeVisible();

  const deadline = Date.now() + 25_000;
  while (Date.now() < deadline) {
    const result = await list.evaluate((el) => {
      const items = Array.from(el.children);
      const stuckOpacity = items.filter((li) => parseFloat(getComputedStyle(li).opacity) < 0.99).length;
      // "Stale" means an animation reporting playState "running" well past
      // when its own configured duration says it should have finished --
      // not merely "any animation is running right now," which a
      // perfectly healthy flip would also show for up to ~250ms.
      const staleAnimations = items.reduce((sum, li) => {
        const stale = li.getAnimations().filter((a) => {
          const duration = Number(a.effect?.getTiming?.().duration) || 0;
          return a.playState === 'running' && Number(a.currentTime) > duration * 2 + 500;
        });
        return sum + stale.length;
      }, 0);
      return { count: items.length, stuckOpacity, staleAnimations };
    });
    expect(result.count, 'row count exceeds the module cap').toBeLessThanOrEqual(5);
    expect(result.stuckOpacity, 'a row is frozen at partial opacity').toBe(0);
    expect(result.staleAnimations, 'an animation never finished').toBe(0);
    await page.waitForTimeout(1000);
  }
});

test('metrics: rapid resource-tab switching never leaves the panel with a stuck or duplicated row', async ({ page }) => {
  test.setTimeout(35_000);
  await page.goto('#/top');

  const panel = page.locator('.top-consumers__panel .top-bar-list');
  await expect(panel.locator('li').first()).toBeVisible();
  const tabs = page.getByRole('tablist', { name: 'Resource' }).getByRole('tab');
  const tabCount = await tabs.count();

  // Rapid-fire, no settling time between clicks -- every switch swaps
  // the WHOLE each-block keyspace (metricKey) at once, so this is
  // specifically trying to catch an add+remove landing in the same
  // reconciliation as the next one starts.
  for (let cycle = 0; cycle < 3 * tabCount; cycle++) {
    await tabs.nth(cycle % tabCount).click();
    const result = await panel.evaluate((el) => {
      const items = Array.from(el.children);
      const names = items.map((li) => li.querySelector('.top-bar-list__name-text')?.textContent ?? '');
      const stuckOpacity = items.filter((li) => parseFloat(getComputedStyle(li).opacity) < 0.99).length;
      return { duplicates: names.length - new Set(names).size, stuckOpacity };
    });
    expect(result.duplicates, 'duplicate entity after a resource switch').toBe(0);
    expect(result.stuckOpacity, 'a row is frozen at partial opacity after a resource switch').toBe(0);
  }

  // One more full pass, this time polling for every flip to actually
  // settle (rather than a single fixed-delay check) -- a shared CI
  // runner under load can easily push a real, healthy 250ms flip's own
  // 'finished' callback out past a fixed few hundred ms with nothing
  // wrong at all, so this waits UP TO a generous timeout, succeeding
  // the moment the count reaches 0. A genuinely stuck animation (the
  // bug this guards against) never reaches 0 no matter how long this
  // waits, so the generous ceiling only costs time in the already-rare
  // failure case, never in the healthy one.
  for (let cycle = 0; cycle < tabCount; cycle++) {
    await tabs.nth(cycle).click();
    await expect
      .poll(
        () =>
          panel.evaluate((el) =>
            Array.from(el.children).reduce(
              (sum, li) => sum + li.getAnimations().filter((a) => a.playState !== 'finished' && a.playState !== 'idle').length,
              0,
            ),
          ),
        { message: 'an animation never finished settling after a resource switch', timeout: 5_000 },
      )
      .toBe(0);
  }
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
