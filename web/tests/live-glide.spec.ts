import { test, expect, type Page } from '@playwright/test';

// Perpetual-glide verification (Scott: "all percentages should be real
// time. metrics page looks like it still has the choppy 2 second
// update"): a live number backed by the shared cadence-driven Tween
// renders many intermediate values BETWEEN the ~2s SSE frame arrivals,
// while an unwired one only ever changes ON an arrival. These tests
// sample rendered text at sub-cadence intervals from inside the page
// (one page.evaluate per window -- no per-sample protocol round-trip
// jitter) and count distinct readings, asserting against a bound
// computed from the ACTUAL elapsed span rather than a fixed number so a
// loaded parallel worker stretching the loop can't flake either side:
// a stepper can't exceed one change per frame arrival (2s cadence --
// bounded here at a conservative 1900ms) plus slack for the arrival
// already in flight, while a glide on a value that moves whole
// percentage points per leg renders a fresh 0.1-step reading on nearly
// every sample. Deliberately headless-Playwright, not the interactive
// browser pane: a hidden pane suspends rAF, which would freeze the very
// glides under test.

// sampleText: n readings of `selector`'s textContent, intervalMs apart,
// plus the actual elapsed span (performance.now(), for the relative
// bounds above).
async function sampleText(page: Page, selector: string, n: number, intervalMs: number) {
  return page.evaluate(
    async ({ selector, n, intervalMs }) => {
      const el = document.querySelector(selector);
      const samples: string[] = [];
      const start = performance.now();
      for (let i = 0; i < n; i++) {
        samples.push(el?.textContent ?? '');
        await new Promise((r) => setTimeout(r, intervalMs));
      }
      return { samples, elapsedMs: performance.now() - start };
    },
    { selector, n, intervalMs },
  );
}

// stepperCeiling: the most distinct readings a NON-gliding (per-arrival
// stepping) value could show across elapsedMs -- one per 1900ms-bounded
// arrival, plus the initial reading and one boundary arrival of slack.
function stepperCeiling(elapsedMs: number): number {
  return Math.floor(elapsedMs / 1900) + 2;
}

test('metrics: the host-total header value glides between frames instead of stepping', async ({ page }) => {
  await page.goto('#/top/cpu');

  const value = page.locator('.top-consumers__header-value');
  await expect(value).toHaveText(/^\d+\.\d%$/);
  // Let the live glide actually start (the very first reading after
  // mount can sit on a seeded value until the next frame lands) --
  // same expect.poll shape as smoke's own "CPU tile ticks" test.
  const initial = await value.textContent();
  await expect.poll(() => value.textContent(), { timeout: 6_000 }).not.toBe(initial);

  const { samples, elapsedMs } = await sampleText(page, '.top-consumers__header-value', 30, 120);
  const distinct = new Set(samples).size;
  // The fake host cpu.total moves whole percentage points per 2s leg, so
  // a linear glide renders a fresh 0.1%-granular reading on nearly every
  // 120ms sample -- far above anything per-arrival stepping can produce.
  expect(distinct, `sampled ${samples.length} readings over ${Math.round(elapsedMs)}ms: ${[...new Set(samples)].join(', ')}`).toBeGreaterThan(
    stepperCeiling(elapsedMs),
  );
});

test('metrics: the hero chart canvas repaints continuously between frames', async ({ page }) => {
  await page.goto('#/top/cpu');
  await expect(page.locator('.top-consumers__header canvas')).toBeVisible();
  await expect.poll(() => page.locator('.top-consumers__chip').count()).toBeGreaterThan(1);

  // Ten canvas snapshots 250ms apart: the live x-window slides (and every
  // line's head eases) on the shared driver's ~30fps tick, so MOST
  // adjacent pairs differ -- not all: a 250ms slide of the 900s window is
  // well under a pixel, so two snapshots can legitimately rasterize
  // identically -- while a chart that only repainted per ~2s SSE frame
  // can't change more than once per arrival. Same elapsed-relative bound
  // as the text sampling above.
  const { changedPairs, elapsedMs } = await page.evaluate(async () => {
    const canvas = document.querySelector('.top-consumers__header canvas') as HTMLCanvasElement;
    const shots: string[] = [];
    const start = performance.now();
    for (let i = 0; i < 10; i++) {
      shots.push(canvas.toDataURL());
      await new Promise((r) => setTimeout(r, 250));
    }
    let changed = 0;
    for (let i = 1; i < shots.length; i++) {
      if (shots[i] !== shots[i - 1]) changed++;
    }
    return { changedPairs: changed, elapsedMs: performance.now() - start };
  });
  // A per-arrival stepper changes at most one adjacent pair per arrival
  // (floor(elapsed / 1900) of them, conservatively) -- gliding repaints
  // clear that with room to spare (~7 of 9 pairs at this spacing).
  expect(changedPairs, `over ${Math.round(elapsedMs)}ms`).toBeGreaterThan(Math.floor(elapsedMs / 1900) + 1);
});

test('storage: the per-disk usage percentage ticks with the live frame', async ({ page }) => {
  await page.goto('#/storage');

  // Liveness only -- fake fs usage drifts ~0.1-0.2pp per tick, too small
  // a per-leg movement for the distinct-count discriminator above to
  // separate glide from step reliably; the glide MECHANISM is the same
  // LiveValue component the header test already pins down.
  const pct = page.locator('.storage-disk__usage-pct').first();
  await expect(pct).toHaveText(/^\d+\.\d%$/);
  const initial = await pct.textContent();
  await expect.poll(() => pct.textContent(), { timeout: 15_000 }).not.toBe(initial);
});

test('compare: member rows still show a live cores annotation next to CPU', async ({ page }) => {
  await page.goto('#/compare/jellyfin,plex');
  await expect(page.locator('.compare__chip')).toHaveCount(2);

  // The cores figure now renders off its own Tween (CompareMemberRow's
  // coresTween) -- this pins that the tween actually receives the live
  // cpu.cores target (a mis-wired target would leave fmtCores(0) = ''
  // and the annotation permanently absent, which nothing else asserts).
  await expect
    .poll(() => page.locator('.compare-member-row__stacked-inline .compare-member-row__muted').first().textContent(), {
      timeout: 10_000,
    })
    .toMatch(/cores/);
});

// Reduced motion keeps every one of these instant and discrete: the
// driver never ticks, every Tween collapses to duration 0, so the header
// value changes ONLY on a frame arrival -- the same sampling window that
// must show many readings above must show very few here. NOTE the
// emulation must go through contextOptions: this Playwright version has
// no top-level `reducedMotion` test option any more, and test.use({
// reducedMotion }) is silently ignored (an un-emulated page glides right
// through the "discrete" assertion below -- reproduced live while
// building this).
test.describe('reduced motion', () => {
  test.use({ contextOptions: { reducedMotion: 'reduce' } });

  test('metrics: the host-total header still ticks, but discretely', async ({ page }) => {
    await page.goto('#/top/cpu');

    const value = page.locator('.top-consumers__header-value');
    await expect(value).toHaveText(/^\d+\.\d%$/);
    // Still live -- reduced motion must never freeze the number, only
    // un-animate it.
    const initial = await value.textContent();
    await expect.poll(() => value.textContent(), { timeout: 6_000 }).not.toBe(initial);

    const { samples, elapsedMs } = await sampleText(page, '.top-consumers__header-value', 30, 120);
    const distinct = new Set(samples).size;
    expect(distinct, `sampled ${samples.length} readings over ${Math.round(elapsedMs)}ms`).toBeLessThanOrEqual(
      stepperCeiling(elapsedMs),
    );
  });
});
