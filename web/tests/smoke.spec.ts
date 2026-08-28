import { test, expect } from '@playwright/test';

// Playwright smoke suite: drives the real built binary (see
// playwright.config.ts's webServer) in GANTRY_FAKE_DATA=1 mode, which
// synthesizes a 20-container demo fleet (internal/fake/fake.go) --
// "jellyfin" below is one of that fleet's fixed archetypes, always
// present and always state=running.
const ROUTES: { hash: string; h1: string }[] = [
  { hash: '#/', h1: 'Overview' },
  { hash: '#/containers', h1: 'Containers' },
  { hash: '#/containers/jellyfin', h1: 'jellyfin' },
  { hash: '#/top', h1: 'Top Consumers' },
  { hash: '#/storage', h1: 'Storage' },
  { hash: '#/gpu', h1: 'GPU' },
  { hash: '#/events', h1: 'Events' },
  { hash: '#/settings', h1: 'Settings' },
];

test('every route renders its h1 landmark', async ({ page }) => {
  for (const r of ROUTES) {
    await page.goto(r.hash);
    await expect(page.locator('h1.page-title')).toHaveText(r.h1);
  }
});

test('overview: status headline reflects fleet/array/disk state, the fleet strip is fully countable, and the CPU tile ticks', async ({
  page,
}) => {
  await page.goto('#/');

  // D2's headline replaces the old fleet-count card: a plain sentence,
  // either the all-clear or a counted "N things need you" -- the fake
  // fleet boots 100% running/healthy (fake.go's Metas()) and its one
  // disk.errors trigger is 5 real minutes into server uptime, well
  // outside this test's own window, so "Everything is running" is the
  // expected reading fresh off a CI-built server; a long-lived reused
  // local dev server (reuseExistingServer, non-CI only) could
  // legitimately have crossed that mark, so both readings are accepted
  // and checked for internal consistency instead of asserting one only.
  const headline = page.locator('.overview__headline-text');
  await expect(headline).toBeVisible();
  const headlineText = await headline.textContent();
  const isAllClear = headlineText === 'Everything is running';
  expect(isAllClear || /^\d+ things? need(s)? you$/.test(headlineText ?? '')).toBe(true);
  await expect(page.locator('.overview__attention')).toHaveCount(isAllClear ? 0 : 1);

  // Fleet strip is D2's own "countable, literal" evidence -- one unit
  // per container, so its count must equal the fleet sentence's own
  // stated total. Deliberately NOT a hardcoded 20: this box's docker
  // collector runs for real alongside fake.go's 20 synthetic
  // archetypes (reproduced live while building this -- a handful of
  // the sandbox's own real containers showed up in the fleet total
  // too), so the true size varies by environment. This instead checks
  // that the strip and the sentence -- two client-side views of the
  // exact same live container set -- agree with each other.
  const fleetSentence = page.locator('.overview__sub-line').first();
  await expect(fleetSentence).toBeVisible();
  const statedTotal = Number((await fleetSentence.textContent())?.match(/^(\d+)/)?.[1]);
  expect(statedTotal).toBeGreaterThan(0);

  const fleetUnits = page.locator('.fleet-strip .fleet-unit');
  await expect.poll(() => fleetUnits.count()).toBe(statedTotal);

  // CPU tile is the first row of Overview's instrument rail. The fake
  // generator writes host cpu.total with real per-tick jitter (see
  // fake.go's Tick), so two samples a few ticks apart should differ --
  // expect.poll (not a fixed sleep) waits only as long as it actually
  // takes, up to the 6s window the brief allows.
  const cpuNumber = page.locator('.overview__metrics-rail .stat-tile').first().locator('.stat-tile__number');
  const initial = await cpuNumber.textContent();
  await expect.poll(() => cpuNumber.textContent(), { timeout: 6_000 }).not.toBe(initial);
});

test('overview: the Top Consumers module metric switcher changes the module and deep-links "View all"', async ({
  page,
}) => {
  await page.goto('#/');

  const topModule = page.locator('.overview__top');
  await expect(topModule).toBeVisible();

  // Fresh context -> no stored preference yet -> CPU is the default.
  await expect(topModule.getByRole('tab', { name: 'CPU', exact: true })).toHaveAttribute('aria-selected', 'true');
  await expect(topModule.locator('.overview__top-link')).toHaveAttribute('href', '#/top/cpu');

  const memTab = topModule.getByRole('tab', { name: 'Mem', exact: true });
  await memTab.click();
  await expect(memTab).toHaveAttribute('aria-selected', 'true');

  // "View all" deep-links to the just-selected resource, and landing on
  // the full #/top view pre-selects that same resource's own (fuller-
  // labeled) tab there too.
  await expect(topModule.locator('.overview__top-link')).toHaveAttribute('href', '#/top/mem');
  await topModule.locator('.overview__top-link').click();
  await expect(page).toHaveURL(/#\/top\/mem$/);
  await expect(page.getByRole('tab', { name: 'Memory', exact: true })).toHaveAttribute('aria-selected', 'true');
});

test('containers: clicking a header sorts the table', async ({ page }) => {
  await page.goto('#/containers');

  // Scoped to the table that has a <thead> -- Containers.svelte renders
  // a second, header-less .containers-table for the (here, always empty)
  // stopped section, and a parallel mobile card list that stays in the
  // DOM (just display:none) at this desktop viewport; an unscoped
  // selector would double-count names from either.
  const nameLinks = page.locator('table.containers-table:has(thead) tbody tr.container-row a');
  await expect.poll(() => nameLinks.count()).toBeGreaterThan(0);

  const before = await nameLinks.allTextContents();
  await page.getByRole('button', { name: 'Sort by Name' }).click();

  await expect.poll(() => nameLinks.allTextContents()).not.toEqual(before);
  const after = await nameLinks.allTextContents();
  expect(after).toEqual([...after].sort((a, b) => a.localeCompare(b)));
});

// Regression coverage for Scott's own report: "when values change, the
// width of the columns change size. this happens constantly and is not
// good looking." table-layout:fixed + the <colgroup> (Containers.svelte)
// mean every column's width comes from ITS OWN fixed spec, never
// recomputed from a row's live content -- so column x-positions must
// stay byte-identical across two real live ticks (2s cadence), even
// though the cell TEXT underneath them keeps changing length
// ("17.2 KB/s" vs "947.6 B/s").
test('containers: column widths do not jitter as live values tick', async ({ page }) => {
  test.setTimeout(30_000);
  await page.goto('#/containers');

  const headers = page.locator('table.containers-table:has(thead) thead th');
  await expect.poll(() => headers.count()).toBeGreaterThan(0);

  async function headerRects() {
    return headers.evaluateAll((ths) => ths.map((th) => ({ left: th.getBoundingClientRect().left, width: th.getBoundingClientRect().width })));
  }

  const before = await headerRects();
  // Comfortably more than one 2s tick, so at least one visible value's
  // rendered string length has actually changed underneath these cells.
  await page.waitForTimeout(6_000);
  const after = await headerRects();

  expect(after).toEqual(before);
});

test('container detail: charts render and the log viewer shows its empty state', async ({ page }) => {
  await page.goto('#/containers/jellyfin');

  await expect(page.locator('.container-detail__charts canvas').first()).toBeVisible({ timeout: 10_000 });

  // "jellyfin" is a fake-fleet name, not a real docker container --
  // dc.StreamLogs errors for it, api_logs.go answers 404, and the log
  // viewer renders its graceful empty state instead of a broken pane.
  await expect(page.locator('.log-viewer__empty')).toContainText('Logs aren\'t available for "jellyfin"', {
    timeout: 10_000,
  });
});

test('top consumers: switching window from Now to 1h renders without erroring', async ({ page }) => {
  await page.goto('#/top');

  // Now is live and client-derived (topFromFrame) -- every fake container
  // reports cpu.pct, so this always has real bars, never the empty state.
  await expect(page.locator('.top-bar-list__row').first()).toBeVisible();

  await page.getByRole('button', { name: '1h', exact: true }).click();
  await expect(page.locator('.top-consumers__loading')).toHaveCount(0, { timeout: 10_000 });

  // No error banner, and the panel settled on either real bars or the
  // window-specific empty state -- both are acceptable outcomes for a
  // freshly-started store that may not have an hour of history yet.
  await expect(page.locator('.top-consumers__error')).toHaveCount(0);
  await expect(page.locator('.top-bar-list__row, .top-bar-list__empty').first()).toBeVisible();
});

// Regression coverage for the top-consumers host-share fix: a container
// used to read docker-stats' own per-core percent (routinely near/at
// 100%) right next to the host tile's much lower whole-machine total --
// confusing, and the bug this branch exists to fix. Both surfaces (the
// full page here, and Overview's compact module below) must show every
// CPU row on the SAME host-share scale as the host CPU tile, with a
// quiet "≈N.N cores" secondary giving back the docker-stats-style number
// for anyone who wants it.
test('top consumers: CPU rows read as a host-share percentage with a quiet cores secondary', async ({ page }) => {
  await page.goto('#/top/cpu');

  const rows = page.locator('.top-bar-list__row');
  await expect(rows.first()).toBeVisible();

  // fake.go's fleet never uses more than one full core (fakeHostCores'
  // own doc), so every row must read comfortably under 100% -- nothing
  // pegged at the old per-core ceiling.
  const values = await rows.locator('.top-bar-list__value').allTextContents();
  expect(values.length).toBeGreaterThan(0);
  for (const text of values) {
    const value = Number(text.replace('%', ''));
    expect(value).toBeGreaterThanOrEqual(0);
    expect(value).toBeLessThan(20);
  }

  // At least one row's secondary renders (a container using well under
  // 0.05 cores hides it entirely -- see format.ts's fmtCores).
  await expect(page.locator('.top-bar-list__secondary').first()).toHaveText(/^≈\d+\.\d cores$/);
});

// Regression coverage for Scott's own report: a quiet 6.5%-busy container
// used to draw a nearly-full bar just because nothing else running was any
// busier -- the leaderboard scaled every bar relative to its OWN top row,
// not to the machine. cpu/gpu are both read on a fixed 0-100 scale (see
// topFromFrame's resourceScaleMax), so a row's bar width and its own
// printed percentage must always agree, on every row, not just the top
// one. Two separate page.goto calls (rather than clicking between tabs on
// one page) sidesteps a separate, already-flagged bug where a container
// present on both leaderboards keeps its prior tab's value for a while
// after a same-page tab switch -- irrelevant to the scale math this pins,
// but no reason to couple this test to it.
test('top consumers: CPU and GPU bar widths read as an absolute fraction of 100, not relative to the busiest row', async ({
  page,
}) => {
  async function assertBarMatchesItsOwnValue(hash: string) {
    await page.goto(hash);
    const rows = page.locator('.top-bar-list__row');
    await expect(rows.first()).toBeVisible();
    const count = await rows.count();
    for (let i = 0; i < count; i++) {
      const { value, actualPct } = await rows.nth(i).evaluate((el) => {
        const value = Number(el.querySelector('.top-bar-list__value')!.textContent!.replace('%', ''));
        const track = el.querySelector('.top-bar-list__track')!.getBoundingClientRect();
        const bar = el.querySelector('.top-bar-list__bar')!.getBoundingClientRect();
        return { value, actualPct: (bar.width / track.width) * 100 };
      });
      // A whole point of slack absorbs the value text's own 1-decimal
      // rounding -- a relative-to-max regression would be off by tens of
      // points on every row but the top one, well outside this.
      expect(actualPct, `${hash} row ${i}: value ${value}%, bar reads ${actualPct.toFixed(1)}%`).toBeGreaterThan(
        value - 1,
      );
      expect(actualPct).toBeLessThan(value + 1);
    }
  }

  await assertBarMatchesItsOwnValue('#/top/cpu');
  await assertBarMatchesItsOwnValue('#/top/gpu');
});

test('overview: the Top Consumers module shows the same host-share cores secondary on CPU rows', async ({ page }) => {
  await page.goto('#/');

  const topModule = page.locator('.overview__top');
  await expect(topModule.locator('.top-bar-list__row').first()).toBeVisible();
  await expect(topModule.locator('.top-bar-list__secondary').first()).toHaveText(/^≈\d+\.\d cores$/);
});

// Regression coverage for Scott's own report: a live box misread its
// boot flash device as HDD and its NVMe pools as generic SSD (rotational
// alone can't tell either apart -- see disks.go's DiskKind doc). Fake
// mode's 8-disk fleet (internal/fake/fake.go) covers all four of
// Storage's own type badges. diskRow anchors on the bare disk name
// (.storage-disk__name's own exact text) rather than matching the whole
// row's text, since two different disks can share the same ROLE label
// text ("Cache / pool", for both "cache" and "rocket_pool").
test('storage: every disk type badge is classified correctly (hdd/ssd/nvme/usb)', async ({ page }) => {
  await page.goto('#/storage');

  const diskRow = (name: string) =>
    page.locator('.storage-disk').filter({ has: page.locator('.storage-disk__name', { hasText: new RegExp(`^${name}$`) }) });

  await expect(diskRow('disk1').locator('.storage-disk__media')).toContainText('HDD');
  await expect(diskRow('cache').locator('.storage-disk__media')).toContainText('SSD');
  await expect(diskRow('rocket_pool').locator('.storage-disk__media')).toContainText('NVMe');
  await expect(diskRow('flash').locator('.storage-disk__media')).toContainText('USB');

  // The boot device's own role label is distinct from a plain pool's --
  // proves ROLE_LABEL and MEDIA_LABEL aren't accidentally conflated.
  await expect(diskRow('flash')).toContainText('Boot (flash)');
});

test('theme toggle flips data-theme and persists across reload', async ({ page }) => {
  await page.emulateMedia({ colorScheme: 'light' });
  await page.goto('#/');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');

  // Cycle order is system -> light -> dark -> system (theme.svelte.ts);
  // starting from system (resolved light, forced above), the first click
  // is a no-visible-op (explicit light == already-resolved light) and
  // the second lands on dark, which the following assertion catches.
  const toggle = page.locator('.theme-toggle');
  await toggle.click();
  await toggle.click();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');

  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
});

test('375px viewport: no route scrolls horizontally', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 800 });
  for (const r of ROUTES) {
    await page.goto(r.hash);
    await expect(page.locator('h1.page-title')).toBeVisible();
    const { scrollWidth, innerWidth } = await page.evaluate(() => ({
      scrollWidth: document.documentElement.scrollWidth,
      innerWidth: window.innerWidth,
    }));
    expect(scrollWidth, `${r.hash}: scrollWidth ${scrollWidth} > innerWidth ${innerWidth}`).toBeLessThanOrEqual(
      innerWidth,
    );
  }
});

// Regression coverage for a 375px-only wrap: "Disk IO" is the one
// two-word label among the resource tabs and used to break onto a
// second line ("Disk" / "IO") once the row got squeezed this narrow --
// the button's own box stays a fixed min-height either way (2 lines of
// this text still fits under it), so this checks the label's own text
// layout directly instead: getClientRects() on a text range reports one
// rect per visual line, so more than one means it wrapped.
test('top consumers: the "Disk IO" resource tab stays one line at 375px', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 800 });
  await page.goto('#/top');

  const diskIoTab = page.getByRole('tab', { name: 'Disk IO', exact: true });
  await expect(diskIoTab).toBeVisible();

  const lineCount = await diskIoTab.evaluate((el) => {
    const range = document.createRange();
    range.selectNodeContents(el);
    return range.getClientRects().length;
  });
  expect(lineCount).toBe(1);
});

test('LivePulse shows live state while frames flow', async ({ page }) => {
  await page.goto('#/');
  await expect(page.locator('.live-pulse__ring')).toBeVisible({ timeout: 5_000 });
  await expect(page.locator('.live-pulse__text')).toHaveText('live · 2s');

  // The ring node is recreated (Svelte {#key live.frameCount}) on every
  // received SSE frame -- waiting for a second, distinct node in that
  // position proves another live frame actually arrived, rather than a
  // fixed sleep that would only prove time passed.
  const firstRing = await page.locator('.live-pulse__ring').elementHandle();
  await page.waitForFunction((prev) => document.querySelector('.live-pulse__ring') !== prev, firstRing, {
    timeout: 6_000,
  });
  await expect(page.locator('.live-pulse__text')).toHaveText('live · 2s');
});

// Smooth-streaming (docs/superpowers/sdd/smooth-streaming) adds a shared
// rAF-driven animation loop for live charts/numbers, but the whole
// feature is a no-op under prefers-reduced-motion -- the driver never
// starts at all (see lib/streamdriver.svelte.ts), and every svelte/
// motion Tween collapses to duration 0. This context is scoped to just
// this describe block (test.use here doesn't affect the suite's other
// tests, which run under normal motion) so it can assert the ONE thing
// that must still be true regardless: data keeps flowing, just without
// any animation smoothing it.
test.describe('reduced motion', () => {
  test.use({ reducedMotion: 'reduce' });

  test('overview still renders and ticks discretely under prefers-reduced-motion', async ({ page }) => {
    await page.goto('#/');
    await expect(page.locator('h1.page-title')).toHaveText('Overview');

    // Same locator/assertion shape as the un-reduced "CPU tile ticks"
    // test above -- reduced motion must not break the underlying SSE
    // data flow, only the animation on top of it.
    const cpuNumber = page.locator('.overview__metrics-rail .stat-tile').first().locator('.stat-tile__number');
    await expect(cpuNumber).toBeVisible();
    const initial = await cpuNumber.textContent();
    await expect.poll(() => cpuNumber.textContent(), { timeout: 6_000 }).not.toBe(initial);
  });
});
