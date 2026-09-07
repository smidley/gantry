import { test, expect } from './fixtures/insight';

// Controlled incidents cover map states; the separate pipeline test and Go
// graph tests cover detection and server-side graph construction.
test.beforeEach(({ scenario }) => scenario.activateAll());

test('map empty state names the absence of contention and its attribution tier', async ({ page, scenario }) => {
  scenario.clearActive();
  await page.goto('#/insights/map');
  await expect(page.locator('.interaction-map__empty-line')).toHaveText('No container is currently contending with another.');
  await expect(page.locator('.interaction-map__empty-tier')).toBeVisible();
});

test('map renders incident nodes, edges, and a legend', async ({ page }) => {
  await page.goto('#/insights/map');
  await expect(page.locator('.interaction-map__canvas svg')).toBeVisible();
  await expect(page.locator('.interaction-map__node')).toHaveCount(5);
  await expect(page.locator('.interaction-map__edge')).toHaveCount(4);
  await expect(page.locator('.interaction-map__legend')).toBeVisible();
});

test('likely edges are dashed and confirmed edges are solid', async ({ page }) => {
  await page.goto('#/insights/map');
  const edges = page.locator('.interaction-map__edge-line');
  await expect(edges).toHaveCount(4);
  const patterns = await edges.evaluateAll(els => els.map(el => getComputedStyle(el).strokeDasharray));
  expect(patterns.filter(pattern => /7(?:px)?[,\s]+6/.test(pattern))).toHaveLength(2);
  expect(patterns.filter(pattern => pattern === 'none')).toHaveLength(2);
});

test('hovering an edge dims unrelated edges', async ({ page }) => {
  await page.goto('#/insights/map');
  const edges = page.locator('.interaction-map__edge');
  await expect(edges).toHaveCount(4);
  // SVG paths can be perfectly horizontal: their geometry has zero height,
  // while their stroke still provides a real pointer target.
  const midpoint = await edges.first().locator('.interaction-map__edge-hit').evaluate((el: SVGPathElement) => {
    const point = el.getPointAtLength(el.getTotalLength() / 2);
    const screen = new DOMPoint(point.x, point.y).matrixTransform(el.getScreenCTM()!);
    return { x: screen.x, y: screen.y };
  });
  await page.mouse.move(midpoint.x, midpoint.y);
  await expect(edges.first()).not.toHaveClass(/interaction-map__edge--dimmed/);
  await expect(edges.nth(1)).toHaveClass(/interaction-map__edge--dimmed/);
});

test('keyboard activation opens the selected incident evidence', async ({ page }) => {
  await page.goto('#/insights/map');
  const edge = page.locator('.interaction-map__edge').first();
  await expect(page.locator('.interaction-map__canvas svg')).toBeVisible();
  await expect(edge).toBeAttached();
  const id = await edge.getAttribute('data-insight-id');
  await edge.focus();
  await expect(edge).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(new RegExp(`#/insights/${id}$`));
  await expect(page.locator('.insight-detail h1.page-title')).not.toBeEmpty();
});

test('reduced motion preserves the map and its controls', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto('#/insights/map');
  await expect(page.locator('.interaction-map__canvas')).toBeVisible();
  await expect(page.locator('.interaction-map__edge').first()).toHaveAttribute('tabindex', '0');
});
