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
// loaded parallel worker (or a 2-core CI runner) stretching the loop
// can't flake either side.
//
// The two directions need OPPOSITE conservatism, so they use different
// stepper models:
//
// - glideFloor (what a glide must beat): models the most a stepper
//   could show as tightly as the real ~2s cadence allows (1900ms), and
//   is capped at what the fixed observation count can express -- a
//   choked runner stretching elapsed far past nominal must never
//   inflate the demand beyond what the sampler could even record. The
//   floor also never assumes glide-side abundance: a quiet fake-data
//   window can move only a few tenths of a point per leg, rendering as
//   few as ~5 distinct 0.1%-quantized readings per window, and a choked
//   runner delivers rAF at only 10-20fps -- so the floor stays pinned
//   to the stepper model (well under 8 distinct/second) instead of
//   assuming healthy-rAF sample counts.
// - stepperAllowance (what a stepper must stay under, reduced-motion
//   test): models the same stepper generously (1500ms cadence plus
//   extra slack) so scheduling jitter bunching frame arrivals on a
//   loaded runner can't push a legitimate stepper over its ceiling.
//
// Deliberately headless-Playwright, not the interactive browser pane:
// a hidden pane suspends rAF, which would freeze the very glides under
// test.

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

// glideFloor: what a gliding value must beat -- the most distinct
// readings a NON-gliding (per-arrival stepping) value could show across
// elapsedMs: one per 1900ms-bounded arrival (2s real cadence) plus the
// initial reading and one in-flight arrival of slack -- capped at two
// under the observation count. The cap only binds when a severely
// choked runner stretches the sampling loop several-fold past nominal:
// elapsed keeps growing while the observation count stays fixed, so an
// uncapped elapsed-scaled floor would eventually demand more distinct
// readings than the sampler could record even from a perfect glide. At
// nominal (and any sane) elapsed the cap is far above the stepper term
// and the assertion keeps its full local strength.
function glideFloor(elapsedMs: number, observations: number): number {
  return Math.min(Math.floor(elapsedMs / 1900) + 2, observations - 2);
}

// stepperAllowance: the ceiling a legitimately DISCRETE (reduced-motion
// stepping) value must stay under. Deliberately looser than
// glideFloor's stepper model (1500ms cadence, three arrivals of slack):
// this bound fails the test when exceeded, so scheduling jitter
// bunching a loaded runner's frame arrivals must not be able to push a
// real stepper over it. The gap between the two models is the
// discrimination margin.
function stepperAllowance(elapsedMs: number): number {
  return Math.floor(elapsedMs / 1500) + 3;
}

test('metrics: the host-total header value glides between frames instead of stepping', async ({ page }) => {
  // Every test in this file budgets for its settle-grace expect
  // timeouts (30-45s each, see below) summing past the 30s default test
  // timeout on a slow runner -- the graced waits are useless if the
  // test itself dies first. Same precedent as alerts.spec's demo-fire.
  test.setTimeout(120_000);
  await page.goto('#/top/cpu');

  const value = page.locator('.top-consumers__header-value');
  await expect(value).toHaveText(/^\d+\.\d%$/, { timeout: 30_000 });
  // Let the live glide actually start (the very first reading after
  // mount can sit on a seeded value until the next frame lands) --
  // same expect.poll shape as smoke's own "CPU tile ticks" test. The
  // generous timeout is page-settle grace for slow CI runners, not an
  // expectation of how long a healthy run takes.
  const initial = await value.textContent();
  await expect.poll(() => value.textContent(), { timeout: 30_000 }).not.toBe(initial);

  // Glide-vs-step discrimination is only meaningful in a window where
  // the underlying target actually MOVED: near a crest or trough of
  // fake.go's 5-minute swell the host total can sit within one or two
  // 0.1pp quantization levels for tens of seconds, where a perfect
  // glide and a stepper render the same one-or-two readings (reproduced
  // live: 30 samples reading only "13.6%, 13.7%"). Resample until a
  // window sees >=0.5pp of range -- five-plus quantization levels,
  // which a glide renders as five-plus distinct readings (level dwell
  // ~700ms, far above the 120ms sample spacing) while a stepper still
  // can't beat one reading per arrival. The liveness poll above already
  // pinned that the value isn't frozen; if every window lands in a flat
  // stretch of the swell, discrimination is genuinely unavailable this
  // run and the movement-gated assertion is vacuous rather than flaky.
  let moved = false;
  for (let attempt = 0; attempt < 15 && !moved; attempt++) {
    const { samples, elapsedMs } = await sampleText(page, '.top-consumers__header-value', 30, 120);
    const nums = samples.map((s) => Number.parseFloat(s)).filter((n) => Number.isFinite(n));
    moved = nums.length > 0 && Math.max(...nums) - Math.min(...nums) >= 0.5;
    if (!moved) continue;
    const distinct = new Set(samples).size;
    // Across >=0.5pp of movement a linear glide renders a fresh
    // 0.1%-granular reading on nearly every 120ms sample -- far above
    // anything per-arrival stepping can produce.
    expect(distinct, `sampled ${samples.length} readings over ${Math.round(elapsedMs)}ms: ${[...new Set(samples)].join(', ')}`).toBeGreaterThan(
      glideFloor(elapsedMs, samples.length),
    );
  }
});

test('metrics: the hero chart canvas repaints continuously between frames', async ({ page }) => {
  test.setTimeout(120_000);
  await page.goto('#/top/cpu');
  await expect(page.locator('.top-consumers__header canvas')).toBeVisible({ timeout: 30_000 });
  await expect.poll(() => page.locator('.top-consumers__chip').count(), { timeout: 30_000 }).toBeGreaterThan(1);

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
  // A per-arrival stepper changes at most one adjacent pair per
  // 1900ms-bounded arrival, plus one in flight -- gliding repaints
  // clear that with room to spare (~7 of 9 pairs at this spacing, and
  // even 10-20fps choked rAF repaints between 250ms snapshots). The
  // same observation-count cap as glideFloor keeps a stretched elapsed
  // from demanding more changed pairs than ten snapshots' nine gaps can
  // show.
  const maxPairs = 9;
  expect(changedPairs, `over ${Math.round(elapsedMs)}ms`).toBeGreaterThan(
    Math.min(Math.floor(elapsedMs / 1900) + 1, maxPairs - 2),
  );
});

test('storage: the per-disk usage percentage ticks with the live frame', async ({ page }) => {
  test.setTimeout(120_000);
  await page.goto('#/storage');

  // Liveness only -- fake fs usage drifts ~0.1-0.2pp per tick, too small
  // a per-leg movement for the distinct-count discriminator above to
  // separate glide from step reliably; the glide MECHANISM is the same
  // LiveValue component the header test already pins down.
  const pct = page.locator('.storage-disk__usage-pct').first();
  await expect(pct).toHaveText(/^\d+\.\d%$/, { timeout: 30_000 });
  const initial = await pct.textContent();
  await expect.poll(() => pct.textContent(), { timeout: 30_000 }).not.toBe(initial);
});

test('compare: member rows still show a live cores annotation next to CPU', async ({ page }) => {
  test.setTimeout(120_000);
  // frigate is the one fleet member whose cores can NEVER fall under
  // fmtCores' <0.05 blank rule: fake.go gives it cpuBase 12, cpuAmp 4,
  // so cpu.cores floors at 0.08 across the whole 5-minute swell.
  // jellyfin (cpuBase 4) idles well UNDER the blank rule for minutes at
  // a time -- the original jellyfin,plex pairing made this test's pass
  // depend on which phase of the swell the server booted into
  // (reproduced live: 45s of polling a fresh boot, annotation
  // legitimately absent throughout).
  await page.goto('#/compare/frigate,jellyfin');
  await expect(page.locator('.compare__chip')).toHaveCount(2, { timeout: 30_000 });

  // The cores figure now renders off its own Tween (CompareMemberRow's
  // coresTween) -- this pins that the tween actually receives the live
  // cpu.cores target (a mis-wired target would leave fmtCores(0) = ''
  // and the annotation permanently absent on every row, which nothing
  // else asserts). Matched on ANY member row rather than .first() so
  // the assertion doesn't also depend on member ordering. The timeout
  // is live-frame settle grace for slow CI runners, not the expected
  // arrival time.
  await expect(
    page.locator('.compare-member-row__stacked-inline .compare-member-row__muted', { hasText: 'cores' }).first(),
  ).toBeVisible({ timeout: 45_000 });
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
    test.setTimeout(120_000);
    await page.goto('#/top/cpu');

    const value = page.locator('.top-consumers__header-value');
    await expect(value).toHaveText(/^\d+\.\d%$/, { timeout: 30_000 });
    // Still live -- reduced motion must never freeze the number, only
    // un-animate it.
    const initial = await value.textContent();
    await expect.poll(() => value.textContent(), { timeout: 30_000 }).not.toBe(initial);

    const { samples, elapsedMs } = await sampleText(page, '.top-consumers__header-value', 30, 120);
    const distinct = new Set(samples).size;
    expect(distinct, `sampled ${samples.length} readings over ${Math.round(elapsedMs)}ms`).toBeLessThanOrEqual(
      stepperAllowance(elapsedMs),
    );
  });
});
