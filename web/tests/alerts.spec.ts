import { test, expect } from '@playwright/test';

// Alerts view (Task 10), rule editor (Task 11), webhook editor, and
// band unification (Task 12) -- driven against the real fake-mode
// binary (playwright.config.ts's webServer), sharing that one server
// instance with every other spec file in this suite.
//
// Two timing realities this file works around, both real (not flakes):
//
// 1. The alert engine's own first Tick lands ~10s after boot (its
//    10s ticker doesn't fire immediately) -- container-unhealthy's
//    boot-seeding for fake mode's permanently-unhealthy "grafana"
//    happens THEN, not before. A test landing on a genuinely-fresh
//    server can see "Nothing is alerting" for those first ~10s. Every
//    assertion that depends on grafana already firing uses a generous
//    (20s) timeout rather than the default 5s.
// 2. The fake-mode alert demo's own schedule (internal/fake/fake.go's
//    alertDemoDiskEntity: fires ~2min after boot, resolves ~6min after
//    boot) may be at ANY point in its lifecycle by the time the
//    demo-fire test below actually runs. It's written to accept
//    either "currently firing" or "already resolved into history" --
//    the same "accept either reading, check internal consistency"
//    posture smoke.spec.ts's own overview headline test already uses
//    for the identical shared-server reason.
//
// The rule-editor/threshold-color/webhook-editor tests below all edit
// shared, server-side config (the same "host-cpu-high" rule, or the
// same webhook target) -- run serially so two of them can never race
// each other's save.

test('alerts view renders its heading and a real Active section (grafana is unhealthy from fake mode boot)', async ({ page }) => {
  await page.goto('#/alerts');
  await expect(page.locator('h1.page-title')).toHaveText('Alerts');
  await expect(page.locator('.alerts-view__channels')).toBeVisible();
  await expect(page.locator('.alerts-view__rules')).toBeVisible();

  const active = page.locator('.alerts-view__active');
  const row = active.locator('.alerts-view__row', { hasText: 'Container unhealthy' });
  await expect(row).toBeVisible({ timeout: 20_000 });

  // container-unhealthy is an EVENT rule -- it carries no metric, so it
  // must never render the threshold row's "value vs threshold" shape
  // (meaningless zeros for an event alert). It must instead show the
  // instance's own summary as real detail text.
  await expect(row.locator('.alerts-view__row-value')).toContainText('unhealthy at boot');
  await expect(row.locator('.alerts-view__row-value')).not.toContainText('vs threshold');
});

test('channels strip renders the real notify (ok) and both fake webhook targets, one healthy one failing', async ({ page }) => {
  await page.goto('#/alerts');
  const channels = page.locator('.alerts-view__channels');
  await expect(channels).toContainText('Unraid notifications');
  await expect(channels).toContainText('Webhook: fake-ok');
  await expect(channels).toContainText('Webhook: fake-fail');
  // The failing target's own verbatim health text (channel_webhook.go's
  // Health()) -- never a generic "error" banner. Needs the engine's
  // first tick plus one dispatch attempt, hence the generous timeout.
  await expect(channels).toContainText('last delivery failed', { timeout: 20_000 });
});

test('silence round-trip: silencing an active row dims it with a countdown, lifting it restores the live control', async ({ page }) => {
  await page.goto('#/alerts');
  const row = page.locator('.alerts-view__row', { hasText: 'Container unhealthy' }).first();
  await expect(row).toBeVisible({ timeout: 20_000 });

  await row.getByRole('button', { name: /^Silence/ }).click();
  await row.getByRole('button', { name: '1h', exact: true }).click();

  await expect(row).toHaveClass(/alerts-view__row--silenced/);
  await expect(row).toContainText('Silenced');
  await expect(row).toContainText('left');
  const lift = row.getByRole('button', { name: 'Lift' });
  await expect(lift).toBeVisible();

  await lift.click();
  await expect(row).not.toHaveClass(/alerts-view__row--silenced/);
  await expect(row.getByRole('button', { name: /^Silence/ })).toBeVisible();
});

// demo-fire: Task 9's own fake-mode contract, end to end through the
// real UI. Polls up to 3 minutes (per the plan's own Task 13 contract
// for this exact check).
test('demo-fire: disk-temp-high on disk4 actually fires through the real engine, and renders in Active or History', async ({
  page,
  request,
  baseURL,
}) => {
  test.setTimeout(3 * 60_000 + 30_000);

  let firingNow = false;
  let seenInHistory = false;
  const deadline = Date.now() + 3 * 60_000;
  while (Date.now() < deadline && !firingNow && !seenInHistory) {
    const snap = await (await request.get(`${baseURL}/api/live/snapshot`)).json();
    firingNow = snap.alerts.firing.some(
      (f: { rule_id: string; entity: string }) => f.rule_id === 'disk-temp-high' && f.entity === 'disk4',
    );
    if (!firingNow) {
      const hist = await (await request.get(`${baseURL}/api/alerts/history?limit=200`)).json();
      seenInHistory = hist.some((h: { rule_id: string; entity: string }) => h.rule_id === 'disk-temp-high' && h.entity === 'disk4');
    }
    if (!firingNow && !seenInHistory) await page.waitForTimeout(3000);
  }
  expect(
    firingNow || seenInHistory,
    'disk-temp-high on disk4 must fire (or have already resolved) within 3 minutes of this test running',
  ).toBe(true);

  await page.goto('#/alerts');
  if (firingNow) {
    const row = page.locator('.alerts-view__row', { hasText: 'Disk temperature high' });
    await expect(row).toBeVisible();
    await expect(row).toContainText('disk4');
    await expect(row).toContainText('°C');
  } else {
    const historyRow = page.locator('.alerts-view__row--history', { hasText: 'Disk temperature high' });
    await expect(historyRow).toBeVisible();
  }

  // Overview's own attention list must link the same concern to this view.
  await page.goto('#/');
  const calloutLink = page.locator('.callout-row__title', { hasText: 'Disk temperature high' });
  if ((await calloutLink.count()) > 0) {
    await expect(calloutLink).toHaveAttribute('href', '#/alerts');
  }
});

// --- Shared-config editors: serialized so two tests can never race the
// same server-side rule/target save. ---------------------------------

test.describe.serial('rule editor and band unification', () => {
  // Located by the row's own stable data-rule-id, NOT a text filter:
  // once editing opens, the rule's name only exists as an <input>
  // VALUE, which textContent/hasText can't see -- a hasText-filtered
  // locator would (and, reproduced live, did) stop matching its own
  // row the instant the form replaced the plain-text summary.
  test('editing a builtin rejects an out-of-range for_seconds with a field message, then a valid save round-trips after reload', async ({
    page,
  }) => {
    await page.goto('#/alerts');
    const row = page.locator('.alerts-view__rule-row[data-rule-id="host-cpu-high"]');
    await expect(row).toBeVisible();
    await row.getByRole('button', { name: 'Edit' }).click();

    const forField = row.getByLabel(/Sustained for/);
    await forField.fill('99999');
    await row.getByRole('button', { name: 'Save' }).click();
    await expect(row.locator('.rule-editor__field-error')).toContainText('between 0 and 3600');

    await forField.fill('120');
    await row.getByRole('button', { name: 'Save' }).click();
    await expect(row.locator('.rule-editor')).toHaveCount(0);

    await page.reload();
    const reopened = page.locator('.alerts-view__rule-row[data-rule-id="host-cpu-high"]');
    await reopened.getByRole('button', { name: 'Edit' }).click();
    await expect(reopened.getByLabel(/Sustained for/)).toHaveValue('120');

    // "Reset to default" only renders in the non-editing summary row --
    // cancel out of the form first.
    await reopened.getByRole('button', { name: 'Cancel' }).click();
    // Restore the default so later tests (and a repeated run of this
    // one) see the rule in its original shape.
    await reopened.getByRole('button', { name: 'Reset to default' }).click();
  });

  test('threshold color follows a rule edit: lowering host-cpu-high recolors the Overview CPU tile without a reload', async ({
    page,
  }) => {
    await page.goto('#/alerts');
    const row = page.locator('.alerts-view__rule-row[data-rule-id="host-cpu-high"]');
    await row.getByRole('button', { name: 'Edit' }).click();

    await row.getByLabel('Warn threshold').fill('0');
    await row.getByLabel('Threshold (fire)').fill('0.001');
    await row.getByLabel('Critical threshold').fill('0.002');
    await row.getByRole('button', { name: 'Save' }).click();
    await expect(row.locator('.rule-editor')).toHaveCount(0);

    await page.goto('#/');
    const cpuValue = page.locator('a[href="#/top/cpu"] .stat-tile__value');
    await expect(cpuValue).toHaveClass(/stat-tile__value--tinted/);

    // Restore the default.
    await page.goto('#/alerts');
    const restoreRow = page.locator('.alerts-view__rule-row[data-rule-id="host-cpu-high"]');
    await restoreRow.getByRole('button', { name: 'Reset to default' }).click();
    await page.goto('#/');
    await expect(page.locator('a[href="#/top/cpu"] .stat-tile__value')).not.toHaveClass(/stat-tile__value--tinted/, {
      timeout: 5000,
    });
  });
});

test.describe.serial('webhook targets editor', () => {
  // Located by data-target-id for the same reason as the rule rows
  // above: a hasText filter loses the row the moment editing replaces
  // its plain-text name with an <input>.
  test('never renders a secret value, and reflects header_set after saving one', async ({ page }) => {
    await page.goto('#/settings');
    const card = page.locator('.settings-webhooks');
    await expect(card).toContainText('Fake mode: always succeeds');
    const row = card.locator('.settings-webhooks__row-wrap[data-target-id="fake-ok"]');
    await expect(row).toContainText('No secret');

    await row.getByRole('button', { name: 'Edit' }).click();
    await row.getByLabel('Header name').fill('X-Test-Secret');
    const headerValueInput = row.getByLabel('Header value');
    await headerValueInput.fill('super-secret-value');

    // The network response after saving must never echo the secret back.
    const [response] = await Promise.all([
      page.waitForResponse((r) => r.url().includes('/api/alerts/webhooks') && r.request().method() === 'PUT'),
      row.getByRole('button', { name: 'Save' }).click(),
    ]);
    const body = await response.text();
    expect(body).not.toContain('super-secret-value');

    await expect(row).toContainText('Secret set');

    // Clean up: clear the secret again so this test is repeatable.
    await row.getByRole('button', { name: 'Edit' }).click();
    await row.getByLabel('Clear stored secret').check();
    await row.getByRole('button', { name: 'Save' }).click();
    await expect(row).toContainText('No secret');
  });
});
