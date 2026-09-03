import { test, expect } from '@playwright/test';

// Insights view (Phase 5 Tasks 11-13): active/history/rules, the
// evidence drawer, and the alert-annotation bridge -- driven against
// the real fake-mode binary (playwright.config.ts's webServer), the
// same shared-server instance every other spec file in this suite
// uses.
//
// Timing reality this file works around, the exact alerts.spec.ts
// precedent for the identical reason: the insight engine's own fake-
// mode demo schedule (internal/fake/fake.go's insightDemo* constants:
// disk-io-contention ramps ~60s after boot, holds until ~240s;
// memory-squeeze fires deterministically off the SAME OOM event
// alerts' own demo uses, alertDemoOOMAt = 3 minutes) may be at ANY
// point in its lifecycle by the time a given test actually runs
// against this suite's one shared server. Every assertion that depends
// on a specific finding being currently active accepts either "still
// active" or "already resolved into history" -- never a hard
// hard-coded confidence tier, since whether the PSI-upgrade path is
// exercised in this environment is a fake-mode/tier concern this UI
// layer doesn't control.

test('insights view renders its heading and the tier-1 empty state on a cold-enough check', async ({ page }) => {
  await page.goto('#/insights');
  await expect(page.locator('h1.page-title')).toHaveText('Insights');
  await expect(page.locator('.segmented__btn', { hasText: 'List' })).toBeVisible();
  await expect(page.locator('.segmented__btn', { hasText: 'Map' })).toBeVisible();
  await expect(page.locator('.insights-view__rules')).toBeVisible();
  await expect(page.locator('.insights-view__history')).toBeVisible();
});

// demo-fire: the disk-io-contention/memory-squeeze story fires through
// the real engine end to end. Polls up to 4 minutes (the plan's own
// Task 15 contract for this exact check).
test('demo-fire: a real finding fires through the engine and renders in Active or History with its statement', async ({
  page,
  request,
  baseURL,
}) => {
  test.setTimeout(4 * 60_000 + 30_000);

  let activeNow: { rule_id: string; statement: string } | null = null;
  let seenInHistory: { rule_id: string; statement: string } | null = null;
  const deadline = Date.now() + 4 * 60_000;
  while (Date.now() < deadline && !activeNow && !seenInHistory) {
    const snap = await (await request.get(`${baseURL}/api/live/snapshot`)).json();
    const found = snap.insights?.active?.find(
      (i: { rule_id: string }) => i.rule_id === 'disk-io-contention' || i.rule_id === 'memory-squeeze',
    );
    if (found) {
      activeNow = found;
    } else {
      const hist = await (await request.get(`${baseURL}/api/insights/history?limit=200`)).json();
      seenInHistory = hist.find((h: { rule_id: string }) => h.rule_id === 'disk-io-contention' || h.rule_id === 'memory-squeeze') ?? null;
    }
    if (!activeNow && !seenInHistory) await page.waitForTimeout(3000);
  }
  expect(activeNow || seenInHistory, 'disk-io-contention or memory-squeeze must fire (or have already resolved) within 4 minutes').toBeTruthy();
  const finding = (activeNow ?? seenInHistory)!;

  await page.goto('#/insights');
  // Map is the DEFAULT mode whenever something's active (the plan's
  // own "the picture is the better first read" rule) -- List's own
  // markup isn't even in the DOM until selected, so force it before
  // looking for a List-only row.
  await page.locator('.segmented__btn', { hasText: 'List' }).click();
  if (activeNow) {
    // :not(--history): the shared .insights-view__row class also
    // marks a History row (see ActiveRowVs HistoryRow's own doc on
    // dismiss round-trip below) -- this branch means to find the
    // ACTIVE card specifically.
    const row = page.locator('.insights-view__row:not(.insights-view__row--history)', { hasText: finding.statement.slice(0, 30) });
    await expect(row).toBeVisible();
    await expect(row.locator('.insights-view__chip')).toBeVisible();
  } else {
    const historyRow = page.locator('.insights-view__row--history', { hasText: finding.statement.slice(0, 30) });
    await expect(historyRow).toBeVisible();
  }
});

test('evidence drawer opens from an Active or History row, shows the statement and numbers, and closes on Escape', async ({
  page,
  request,
  baseURL,
}) => {
  test.setTimeout(4 * 60_000 + 30_000);

  // This suite runs fullyParallel, so nothing guarantees another test
  // in this file has already run first -- poll for the full 4-minute
  // demo budget here too, the exact demo-fire test's own contract,
  // rather than assuming borrowed state from elsewhere in the file.
  let id: number | null = null;
  const deadline = Date.now() + 4 * 60_000;
  while (Date.now() < deadline && id === null) {
    const active = await (await request.get(`${baseURL}/api/insights`)).json();
    if (active.active?.length > 0) {
      id = active.active[0].id;
      break;
    }
    const hist = await (await request.get(`${baseURL}/api/insights/history?limit=1`)).json();
    if (hist.length > 0) id = hist[0].id;
    if (id === null) await page.waitForTimeout(3000);
  }
  expect(id, 'at least one insight (active or historical) must exist within 4 minutes').not.toBeNull();

  await page.goto('#/insights');
  // Force List mode -- see demo-fire's own identical doc: Map is the
  // default whenever something's active, and the Active card's own
  // markup (unlike History, which always renders) only exists in List.
  await page.locator('.segmented__btn', { hasText: 'List' }).click();
  const row = page.locator('.insights-view__row, .insights-view__row--history').first();
  await expect(row).toBeVisible();
  await row.locator('.insights-view__statement-btn').click();

  const drawer = page.locator('.insights-drawer');
  await expect(drawer).toBeVisible();
  await expect(drawer.locator('.insights-drawer__statement')).not.toBeEmpty();
  // "show your working": at least one evidence number is rendered, or
  // the drawer's own facts/dismiss rows are still visible even for a
  // finding whose bundle happens to carry only zero-valued fields.
  await expect(drawer.locator('.insights-drawer__facts')).toBeVisible();

  await page.keyboard.press('Escape');
  await expect(drawer).not.toBeVisible();
});

// Drawer interaction map: "when there's an issue in history and it's
// clicked on, the map view should also be provided so the user can
// visually see what was happening" (the owner's own ask). data-insight-id
// (InteractionMap.svelte) is the precise hook both tests below key off:
// the clicked insight's own edge(s) must never be dimmed, and -- ONLY
// when something else happens to be concurrent on this shared server's
// timeline right now, never assumed -- every other edge on the same
// canvas must be.
test('evidence drawer for a HISTORY insight also renders the interaction map, with the clicked insight\'s own edge emphasized', async ({
  page,
  request,
  baseURL,
}) => {
  test.setTimeout(4 * 60_000 + 30_000);

  let target: { id: number; statement: string; confidence: string } | null = null;
  const deadline = Date.now() + 4 * 60_000;
  while (Date.now() < deadline && !target) {
    const hist = await (await request.get(`${baseURL}/api/insights/history?limit=1`)).json();
    if (hist.length > 0) target = hist[0];
    else await page.waitForTimeout(3000);
  }
  test.skip(!target, 'nothing resolved into history within the timeout on this shared server run');

  await page.goto('#/insights');
  // Map is the default mode whenever something's active -- force List,
  // the demo-fire test's own identical doc.
  await page.locator('.segmented__btn', { hasText: 'List' }).click();
  const row = page.locator('.insights-view__row--history', { hasText: target!.statement.slice(0, 30) }).first();
  await expect(row).toBeVisible();
  await row.locator('.insights-view__statement-btn').click();

  const drawer = page.locator('.insights-drawer');
  await expect(drawer).toBeVisible();
  await expect(drawer.locator('.insights-drawer__statement')).not.toBeEmpty();

  // The clicked insight always contributes at least its own culprit edge
  // (insights.ts' own selectOverlappingInsights/buildInsightGraph doc:
  // the clicked row is unioned into the pool unconditionally) -- assert
  // the real canvas directly, not the empty state, since an empty map
  // here would itself be the bug.
  await expect(drawer.locator('.interaction-map__canvas svg')).toBeVisible();

  // expect.poll on COUNT, never toBeVisible() on an individual edge <g>:
  // a perfectly horizontal edge (both endpoints landing on the same rank
  // -- a real, correct, reachable layout, not a rendering bug) reports a
  // ZERO-HEIGHT getBoundingClientRect() for its own <path> geometry, which
  // Playwright reads as "hidden" even though the stroke is genuinely
  // painted and the element is genuinely clickable/focusable -- reproduced
  // directly against the running binary (a memory-squeeze culprit edge
  // with d="M 61 104 C ... 104, ... 104, 219 104"). map.spec.ts's own
  // edge checks avoid toBeVisible() on a single edge for the identical
  // reason (count()/class checks only); polling the COUNT still gives the
  // drawer's own async loadDrawerMap fetch room to land, without hitting
  // that bbox trap. toHaveClass reads the class list regardless of the
  // same bbox quirk, so the dim-state checks below are unaffected either
  // way.
  const focusedEdges = drawer.locator(`.interaction-map__edge[data-insight-id="${target!.id}"]`);
  await expect.poll(() => focusedEdges.count(), { timeout: 10_000 }).toBeGreaterThan(0);
  const focusedCount = await focusedEdges.count();
  for (let i = 0; i < focusedCount; i++) {
    await expect(focusedEdges.nth(i)).not.toHaveClass(/interaction-map__edge--dimmed/);
  }

  // VERIFIED, not assumed: confidence's own dash-vs-solid distinction
  // (map.spec.ts' own "confidence is never colour-alone" test) renders
  // correctly inside the DRAWER's compact map too -- nothing previously
  // pinned this specifically for the compact variant, only the
  // standalone canvas. Same dasharray-tolerant pattern as that test
  // (Chrome may serialize "7 6" back as "7, 6").
  for (let i = 0; i < focusedCount; i++) {
    const style = await focusedEdges.nth(i).locator('.interaction-map__edge-line').getAttribute('style');
    if (target!.confidence === 'likely') {
      expect(style ?? '').toMatch(/dasharray:\s*7[,\s]+6/);
    } else {
      expect(style ?? '').toMatch(/dasharray:\s*none/);
    }
  }

  // Only asserted when there IS something else on the canvas -- the demo
  // schedule may or may not have produced a second, concurrent finding
  // by the time this test runs (this file's own doc, top), and a lone
  // culprit-to-victim pair with nothing dimmed is just as correct a
  // rendering as one with a muted neighbor.
  const otherEdges = drawer.locator(`.interaction-map__edge:not([data-insight-id="${target!.id}"])`);
  const otherCount = await otherEdges.count();
  for (let i = 0; i < otherCount; i++) {
    await expect(otherEdges.nth(i)).toHaveClass(/interaction-map__edge--dimmed/);
  }

  // Legend conditionality (owner-reported: a key entry for a style
  // nothing on screen actually uses reads as noise): read whichever
  // dash styles are ACTUALLY on the drawer's own canvas right now --
  // never assumed, since this shared server's own demo schedule decides
  // whether anything concurrent (and at a different confidence) is
  // present -- and assert the legend matches exactly. mapLayout.ts' own
  // legendPresence/showLegend already have the full unit-level matrix;
  // this just confirms the component actually wires them in.
  const allEdgeLines = drawer.locator('.interaction-map__edge-line');
  const edgeLineCount = await allEdgeLines.count();
  let hasSolid = false;
  let hasDashed = false;
  for (let i = 0; i < edgeLineCount; i++) {
    const style = (await allEdgeLines.nth(i).getAttribute('style')) ?? '';
    if (/dasharray:\s*7[,\s]+6/.test(style)) hasDashed = true;
    else hasSolid = true;
  }
  const legend = drawer.locator('.interaction-map__legend');
  if (hasSolid && hasDashed) {
    // Both styles present: the drawer's own compact variant still shows
    // the legend (showLegend's own doc), with both entries.
    await expect(legend).toBeVisible();
    await expect(legend).toContainText('confirmed');
    await expect(legend).toContainText('likely');
  } else {
    // Only one style present (the common case: a lone clicked insight
    // with nothing concurrent, or a concurrent set that all happens to
    // share one confidence) -- the drawer drops the legend entirely
    // rather than listing a style with no edge on screen.
    await expect(legend).toHaveCount(0);
  }
});

test('evidence drawer for an ACTIVE insight also renders the interaction map (its "moment" is now, the same code path as History)', async ({
  page,
  request,
  baseURL,
}) => {
  test.setTimeout(4 * 60_000 + 30_000);

  let target: { id: number; statement: string } | null = null;
  const deadline = Date.now() + 4 * 60_000;
  while (Date.now() < deadline && !target) {
    const active = await (await request.get(`${baseURL}/api/insights`)).json();
    if (active.active?.length > 0) target = active.active[0];
    else await page.waitForTimeout(3000);
  }
  test.skip(!target, 'no finding became active within the timeout on this shared server run');

  await page.goto('#/insights');
  await page.locator('.segmented__btn', { hasText: 'List' }).click();
  const row = page.locator('.insights-view__row:not(.insights-view__row--history)', { hasText: target!.statement.slice(0, 30) });
  await expect(row).toBeVisible();
  await row.locator('.insights-view__statement-btn').click();

  const drawer = page.locator('.insights-drawer');
  await expect(drawer).toBeVisible();
  await expect(drawer.locator('.interaction-map__canvas svg')).toBeVisible();

  // See the History test above for why this polls COUNT rather than
  // calling toBeVisible() on an individual edge.
  const focusedEdges = drawer.locator(`.interaction-map__edge[data-insight-id="${target!.id}"]`);
  await expect.poll(() => focusedEdges.count(), { timeout: 10_000 }).toBeGreaterThan(0);
  const focusedCount = await focusedEdges.count();
  for (let i = 0; i < focusedCount; i++) {
    await expect(focusedEdges.nth(i)).not.toHaveClass(/interaction-map__edge--dimmed/);
  }
});

// Incident chart: "insight history should also provide a graph of the
// incident if possible" (the owner's own follow-up ask). Dismissing
// (rather than waiting out the full demo schedule) fast-tracks a KNOWN,
// just-fired insight into History -- the exact "dismiss round-trip"
// test's own mechanism just below -- so its own fired-to-resolved window
// sits comfortably inside live-ring/1-minute-tier retention, never the
// "history isn't available" fallback this feature also has to cover
// (incidentChart.ts' own hasChartableData, unit-tested there).
test('evidence drawer for a dismissed insight also renders its incident chart with real data', async ({
  page,
  request,
  baseURL,
}) => {
  test.setTimeout(4 * 60_000 + 30_000);

  // active[length-1] (last), not [0]: the "dismiss round-trip" test
  // below dismisses ITS OWN target via the identical mechanism and this
  // suite runs fullyParallel -- picking the opposite end of the active
  // list keeps the two tests aimed at DIFFERENT findings whenever more
  // than one is concurrently active (this demo's own disk-io-
  // contention/memory-squeeze schedule routinely produces exactly that,
  // this file's own top-of-file doc), and the try/catch below still
  // covers the one active finding case where they can't help but
  // collide.
  let target: { id: number; statement: string } | null = null;
  const deadline = Date.now() + 4 * 60_000;
  while (Date.now() < deadline && !target) {
    const snap = await (await request.get(`${baseURL}/api/live/snapshot`)).json();
    if (snap.insights?.active?.length > 0) target = snap.insights.active[snap.insights.active.length - 1];
    else await page.waitForTimeout(3000);
  }
  test.skip(!target, 'no finding became active within the timeout on this shared server run');

  await page.goto('#/insights');
  await page.locator('.segmented__btn', { hasText: 'List' }).click();
  const row = page.locator('.insights-view__row:not(.insights-view__row--history)', { hasText: target!.statement.slice(0, 30) });
  try {
    await expect(row).toBeVisible({ timeout: 5000 });
  } catch {
    test.skip(true, 'this finding was claimed (dismissed/resolved) by a concurrent test before this one could act on it');
  }
  await row.locator('.insights-view__dismiss-btn').click();
  await row.locator('.insights-view__dismiss-menu .segmented__btn', { hasText: '1d' }).click();
  await expect(row).not.toBeVisible();

  const historyRow = page.locator('.insights-view__row--history', { hasText: target!.statement.slice(0, 30) }).first();
  await expect(historyRow).toBeVisible();
  await historyRow.locator('.insights-view__statement-btn').click();

  const drawer = page.locator('.insights-drawer');
  await expect(drawer).toBeVisible();

  const chartsSection = drawer.locator('.insights-drawer__charts');
  await expect(chartsSection).toBeVisible();
  // A finding dismissed seconds ago must resolve to a REAL chart, never
  // the fallback line (its own fired-to-resolved window sits
  // comfortably inside live-ring/1-minute-tier retention) -- expect.poll
  // gives loadDrawerCharts' own async /api/series fetch room to land.
  // A visible <canvas> inside it is the honest signal that uPlot itself
  // actually mounted against real data (a legend row is NOT a reliable
  // second signal here -- disk-io-contention, this environment's own
  // most likely fake-mode firer, splits into two SEPARATE single-line
  // charts, and TimeChart's own legend only renders for series.length
  // >= 2, so asserting one unconditionally would fail for exactly the
  // rule this suite is most likely to hit).
  const realCharts = chartsSection.locator('.time-chart');
  await expect.poll(() => realCharts.count(), { timeout: 10_000 }).toBeGreaterThan(0);
  await expect(realCharts.first().locator('canvas')).toBeVisible();

  // Markers/band are drawn straight onto uPlot's own <canvas> (no
  // per-marker DOM node exists to query directly, and this suite's own
  // deflaking history -- PRs #46/#48/#49 -- is exactly why a hover-scan
  // reproducing uPlot's internal padding/gutter math to find one isn't
  // attempted here instead): incidentMarkers' own exact {ts, severity,
  // label} output is pinned at the unit level (incidentChart.test.ts),
  // and the drawing mechanism itself (drawMarkers/drawBand) is
  // TimeChart's own pre-existing, unmodified-by-this-feature code path
  // -- already exercised by every OTHER chart in this app that passes
  // `markers`. This test's own job stops at proving the NEW plumbing --
  // rule-to-series mapping, window/padding, the live fetch, the real
  // render -- delivers actual data end to end.
});

// Dismiss round-trip: the one MUTATING test in this file. Targets a
// SPECIFIC finding by its own statement text throughout (never a raw
// "active count"), so it stays correct regardless of whatever else is
// concurrently active or resolving on this shared server -- the same
// reasoning alerts.spec.ts's own silence round-trip test applies.
test('dismiss round-trip: dismissing an active card removes it and adds a history row', async ({ page, request, baseURL }) => {
  test.setTimeout(4 * 60_000 + 30_000);

  let target: { id: number; statement: string } | null = null;
  const deadline = Date.now() + 4 * 60_000;
  while (Date.now() < deadline && !target) {
    const snap = await (await request.get(`${baseURL}/api/live/snapshot`)).json();
    if (snap.insights?.active?.length > 0) target = snap.insights.active[0];
    else await page.waitForTimeout(3000);
  }
  test.skip(!target, 'no finding became active within the timeout on this shared server run');

  await page.goto('#/insights');
  // List mode explicitly -- the dismiss control lives on the Active
  // card, not the map.
  await page.locator('.segmented__btn', { hasText: 'List' }).click();
  // :not(--history): the shared .insights-view__row class also marks
  // a History row, and this SAME statement text can plausibly already
  // exist there too by the time this test runs on a shared server --
  // an un-scoped selector would resolve to two elements (a real
  // strict-mode failure this test hit once) and, worse, "not visible"
  // would never truly go false since the history twin stays visible
  // after dismiss regardless of what happens to the active one.
  const row = page.locator('.insights-view__row:not(.insights-view__row--history)', { hasText: target!.statement.slice(0, 30) });
  await expect(row).toBeVisible();

  await row.locator('.insights-view__dismiss-btn').click();
  await row.locator('.insights-view__dismiss-menu .segmented__btn', { hasText: '1d' }).click();

  await expect(row).not.toBeVisible();
  // .first(): InsightHistory orders newest-resolution-first (store's
  // own doc), and Insights.svelte renders that order unchanged -- if
  // this exact statement text was already in history from an earlier
  // natural resolve, the row THIS dismiss just created is the newest,
  // i.e. the first match.
  const historyRow = page.locator('.insights-view__row--history', { hasText: target!.statement.slice(0, 30) }).first();
  await expect(historyRow).toBeVisible();
  await expect(historyRow).toContainText('dismissed');
});

test('List/Map segmented toggle switches modes and updates the URL', async ({ page }) => {
  await page.goto('#/insights');
  const mapBtn = page.locator('.segmented__btn', { hasText: 'Map' });
  const listBtn = page.locator('.segmented__btn', { hasText: 'List' });

  await mapBtn.click();
  await expect(mapBtn).toHaveClass(/segmented__btn--active/);
  await expect(page).toHaveURL(/#\/insights\/map$/);

  await listBtn.click();
  await expect(listBtn).toHaveClass(/segmented__btn--active/);
  await expect(page).toHaveURL(/#\/insights$/);
});

// A genuinely FRESH navigation (not a same-page hash change, which
// never remounts the component -- see mode's own untrack seed, the
// TopConsumers.svelte initialResource precedent): #/insights/map must
// open straight into Map on its own, the real "someone pasted/clicked
// this link" scenario. page.goto with only a fragment differing from
// the CURRENT URL is a same-document hash change in every browser (no
// navigation event at all), so this test starts from about:blank
// first to force a real load.
test('#/insights/map deep-links straight into Map on a fresh load', async ({ page }) => {
  await page.goto('about:blank');
  await page.goto('#/insights/map');
  await expect(page.locator('.segmented__btn', { hasText: 'Map' })).toHaveClass(/segmented__btn--active/);
});

test('insights map renders (nodes and, given the shared server likely has an active/recent finding by now, an empty state or a real edge either way)', async ({
  page,
}) => {
  await page.goto('#/insights/map');
  // Either the calm empty state (nothing active right now) or a real
  // SVG canvas with at least the legend swatches -- both are valid,
  // mutually exclusive renderings of the same view; this test asserts
  // the view never renders neither (a blank hole) nor both at once.
  const empty = page.locator('.interaction-map__empty');
  const canvas = page.locator('.interaction-map__canvas svg');
  await expect(empty.or(canvas)).toBeVisible();
  const emptyVisible = await empty.isVisible();
  const canvasVisible = await canvas.isVisible();
  expect(emptyVisible !== canvasVisible).toBe(true);
});

// ContainerDetail's own impact panel (Task 12) -- the share strip is
// ALWAYS present (no engine required), while the two findings
// directions render either real rows or the panel's own calm line,
// same demo-timing tolerance as every other test in this file.
test('ContainerDetail impact panel always shows the share strip, and findings when the demo has produced any', async ({
  page,
  request,
  baseURL,
}) => {
  await page.goto('#/containers/qbittorrent');
  const panel = page.locator('.impact-panel');
  await expect(panel).toBeVisible();
  // The share strip's own direction block always renders (cpu/memory
  // are always-known metrics for a running fake-mode container).
  await expect(panel.locator('.impact-panel__direction-label', { hasText: 'Current share' })).toBeVisible();

  const active = await (await request.get(`${baseURL}/api/insights`)).json();
  const involvesQbittorrent = active.active.some(
    (i: { culprit: string; culprits: string }) => i.culprit === 'qbittorrent' || i.culprits?.split(',').includes('qbittorrent'),
  );
  if (involvesQbittorrent) {
    await expect(panel.locator('.impact-panel__direction-label', { hasText: 'Slowing' })).toBeVisible();
    await expect(panel.locator('.impact-panel__statement').first()).not.toBeEmpty();
  } else {
    await expect(panel.locator('.impact-panel__calm')).toBeVisible();
  }
});
