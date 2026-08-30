import { test, expect } from '@playwright/test';

// Maintenance: Gantry's first WRITE-surface view (image + non-running
// container cleanup, plus a read-only Updates card). Every destructive
// action routes through ConfirmDialog, which states exactly what's about
// to be deleted before anything happens -- these tests exercise that
// contract directly (dialog content, cancel-is-a-no-op, then confirm)
// rather than just checking the end state.
//
// Fixture budget: unlike groups.spec.ts's own user-created groups (each
// test deletes what it made), fake mode's image/container maintenance
// seeds (internal/fake/images.go, containers_maintenance.go) are fixed
// and finite for a server's whole lifetime -- there's no create-a-
// fresh-one endpoint to clean up with. The two "lifecycle" describe.serial
// blocks below consume that budget in a fixed order, one seed entry per
// assertion, never reused across tests within the block -- correct
// exactly once against a freshly-started server (every CI run, since
// playwright.config's reuseExistingServer is false there); a repeated
// LOCAL run against an already-mutated server will exhaust it and start
// failing, the same tradeoff any suite covering a real, non-renewable
// resource accepts. Every other test below stays non-destructive
// (mocked responses, or cancel-only) and is safe to rerun any number of
// times.

test.describe.serial('images maintenance: lifecycle', () => {
  test('in-use images never appear in the removable table', async ({ page }) => {
    await page.goto('#/maintenance');
    const card = page.locator('.maintenance-card', { hasText: 'Images' });
    await expect(card.locator('tr', { hasText: 'jellyfin' })).toHaveCount(0);
  });

  test('remove selected: dialog states the exact item/size, cancel is a no-op, confirm removes it and updates the summary', async ({
    page,
  }) => {
    await page.goto('#/maintenance');
    const card = page.locator('.maintenance-card', { hasText: 'Images' });
    const row = card.locator('tr', { hasText: 'redis:7-alpine' });
    await expect(row).toBeVisible();
    await expect(row.locator('input[type="checkbox"]')).not.toBeChecked(); // never pre-checked
    await row.locator('input[type="checkbox"]').check();
    await expect(card.getByRole('button', { name: 'Remove selected (1)' })).toBeVisible();

    await card.getByRole('button', { name: /^Remove selected/ }).click();
    const dialog = page.locator('.confirm-dialog');
    await expect(dialog).toContainText('Remove 1 image?');
    await expect(dialog).toContainText('redis:7-alpine');
    await expect(dialog).toContainText('40.1 MiB');

    // Cancel first -- must be a genuine no-op: nothing removed, still selected.
    await dialog.getByRole('button', { name: 'Cancel' }).click();
    await expect(dialog).toHaveCount(0);
    await expect(row).toBeVisible();
    await expect(row.locator('input[type="checkbox"]')).toBeChecked();

    await card.getByRole('button', { name: /^Remove selected/ }).click();
    await page.locator('.confirm-dialog').getByRole('button', { name: 'Remove 1 image' }).click();

    await expect(card.getByText('✓ redis:7-alpine')).toBeVisible();
    await expect(card.getByRole('link', { name: /logged to Events/ })).toHaveAttribute('href', '#/events');
    await expect(row).toHaveCount(0);
    await expect(card).toContainText('unused 3');
  });

  test('prune dangling: confirm dialog lists every currently-dangling image, then removes all of them', async ({ page }) => {
    await page.goto('#/maintenance');
    const card = page.locator('.maintenance-card', { hasText: 'Images' });
    await expect(card).toContainText('dangling 4');
    await card.getByRole('button', { name: /^Prune dangling/ }).click();

    const dialog = page.locator('.confirm-dialog');
    await expect(dialog).toContainText('Prune 4 dangling images?');
    await expect(dialog.locator('.confirm-dialog__item-primary')).toHaveCount(4);
    await dialog.getByRole('button', { name: 'Prune 4 images' }).click();

    await expect(card).toContainText('dangling 0');
    await expect(card.locator('.maintenance-badge', { hasText: 'dangling' })).toHaveCount(0);
    await expect(card.locator('.maintenance-results__row--error')).toHaveCount(0);
  });

  test('prune unused: removes the rest, including the digest-pinned image', async ({ page }) => {
    await page.goto('#/maintenance');
    const card = page.locator('.maintenance-card', { hasText: 'Images' });
    // 4 originally, minus the one the first test already removed by hand.
    await expect(card).toContainText('unused 3');
    await card.getByRole('button', { name: /^Prune unused/ }).click();

    const dialog = page.locator('.confirm-dialog');
    await expect(dialog).toContainText('Prune 3 unused images?');
    await expect(dialog).toContainText('prowlarr@sha256'); // the digest-pinned fixture
    await dialog.getByRole('button', { name: 'Prune 3 images' }).click();

    await expect(card).toContainText('unused 0');
    await expect(card.locator('tbody tr')).toHaveCount(0);
  });
});

test.describe.serial('containers maintenance: lifecycle', () => {
  test('the running-conflict fixture always refuses removal with a human-readable error, and stays listed', async ({ page }) => {
    await page.goto('#/maintenance');
    const card = page.locator('.maintenance-card', { hasText: 'Containers' });
    const row = card.locator('tr', { hasText: 'watchtower' });
    await expect(row).toBeVisible();
    await expect(row.locator('.maintenance-badge')).toHaveCount(0); // no keep-warning on this one

    await row.locator('input[type="checkbox"]').check();
    await card.getByRole('button', { name: /^Remove selected/ }).click();
    await page.locator('.confirm-dialog').getByRole('button', { name: 'Remove 1 container' }).click();

    await expect(card.getByText('✕ watchtower')).toBeVisible();
    await expect(card).toContainText('container is running');
    await expect(row).toBeVisible(); // refused -- still there
  });

  test('managed/compose containers show their own keep-warning badge, default unchecked, and are removable when explicitly picked', async ({
    page,
  }) => {
    await page.goto('#/maintenance');
    const card = page.locator('.maintenance-card', { hasText: 'Containers' });

    const duplicati = card.locator('tr', { hasText: 'duplicati' });
    await expect(duplicati.locator('.maintenance-badge', { hasText: 'Unraid template' })).toBeVisible();
    await expect(duplicati.locator('input[type="checkbox"]')).not.toBeChecked();

    const prowlarr = card.locator('tr', { hasText: 'prowlarr' });
    await expect(prowlarr.locator('.maintenance-badge', { hasText: 'Compose: media' })).toBeVisible();

    await duplicati.locator('input[type="checkbox"]').check();
    await card.getByRole('button', { name: /^Remove selected/ }).click();
    const dialog = page.locator('.confirm-dialog');
    await expect(dialog).toContainText('duplicati');
    await expect(dialog.locator('.confirm-dialog__item-warning')).toHaveText('Unraid template');
    await dialog.getByRole('button', { name: 'Remove 1 container' }).click();

    await expect(card.getByText('✓ duplicati')).toBeVisible();
    await expect(duplicati).toHaveCount(0);
  });

  test('prune exited: sweeps the remaining exited containers, refusing the running-conflict fixture again', async ({ page }) => {
    await page.goto('#/maintenance');
    const card = page.locator('.maintenance-card', { hasText: 'Containers' });
    await expect(card).toContainText('exited 3'); // watchtower + prowlarr + vaultwarden left
    await card.getByRole('button', { name: /^Prune exited/ }).click();

    const dialog = page.locator('.confirm-dialog');
    await expect(dialog).toContainText('Prune 3 exited containers?');
    await expect(dialog).toContainText('orphaned, not removed'); // the anonymous-volume caveat
    await dialog.getByRole('button', { name: 'Prune 3 containers' }).click();

    await expect(card.getByText('✓ prowlarr')).toBeVisible();
    await expect(card.getByText('✓ vaultwarden')).toBeVisible();
    await expect(card.getByText('✕ watchtower')).toBeVisible();
    await expect(card).toContainText('exited 1');
    await expect(card.locator('tr', { hasText: 'watchtower' })).toBeVisible();
  });

  test('prune created respects the age filter: only the older runner is actually removed', async ({ page }) => {
    await page.goto('#/maintenance');
    const card = page.locator('.maintenance-card', { hasText: 'Containers' });
    await expect(card).toContainText('created 2');

    // With the filter left blank (0 = no filter), the dialog carries no
    // age caveat at all -- it's an exact preview, same as every other
    // prune dialog with nothing left ambiguous.
    await card.getByRole('button', { name: /^Prune created/ }).click();
    let dialog = page.locator('.confirm-dialog');
    await expect(dialog).toContainText('Prune 2 created containers?');
    await expect(dialog.locator('.confirm-dialog__caveat')).toHaveCount(0);
    await dialog.getByRole('button', { name: 'Cancel' }).click();

    await card.locator('input[type="number"]').fill('1');
    await card.getByRole('button', { name: /^Prune created/ }).click();
    dialog = page.locator('.confirm-dialog');
    await expect(dialog).toContainText('Only containers older than 1h will actually be removed');
    await dialog.getByRole('button', { name: 'Prune 2 containers' }).click();

    await expect(card.getByText('✓ github-runner-a1c9f2')).toBeVisible();
    // Never attempted -- not in the result list (the desktop table AND
    // the mobile card list both still carry a real row for it, since it
    // survived, so this checks .maintenance-results specifically rather
    // than the whole card).
    await expect(card.locator('.maintenance-results').getByText('github-runner-77bd0e')).toHaveCount(0);
    await expect(card).toContainText('created 1');
    await expect(card.locator('.maintenance-table tr', { hasText: 'github-runner-77bd0e' })).toBeVisible();
  });
});

test('read-only mode disables every action on both cards with a quiet explanation', async ({ page }) => {
  // probeReadOnly (api.ts) posts a deliberately-invalid mode to /api/
  // images/prune -- the server 403s that BEFORE decoding the body when
  // GANTRY_READ_ONLY is set, never touching a real image. Mocking just
  // that one response (letting every other request through untouched)
  // exercises the read-only UI without a second live server.
  await page.route('**/api/images/prune', async (route) => {
    const body = route.request().postDataJSON();
    if (body?.mode === '__gantry_probe__') {
      await route.fulfill({ status: 403, contentType: 'application/json', body: JSON.stringify({ error: 'read-only mode' }) });
      return;
    }
    await route.continue();
  });

  await page.goto('#/maintenance');
  await expect(page.getByText(/read-only mode.*GANTRY_READ_ONLY/)).toBeVisible();

  const buttons = page.locator('.maintenance-actions button');
  const count = await buttons.count();
  expect(count).toBeGreaterThan(0);
  for (let i = 0; i < count; i++) {
    await expect(buttons.nth(i)).toBeDisabled();
  }
  const checkboxes = page.locator('.maintenance-table-wrap input[type="checkbox"]');
  const checkboxCount = await checkboxes.count();
  expect(checkboxCount).toBeGreaterThan(0);
  for (let i = 0; i < checkboxCount; i++) {
    await expect(checkboxes.nth(i)).toBeDisabled();
  }
});

test('a restart_policy hint renders as an amber "restarts on boot" badge, distinct from the managed badge, and defaults unchecked', async ({
  page,
}) => {
  // Fake mode's own seed never sets RestartPolicy on any fixture (only
  // Managed has variety there) -- mocking the GET response is the only
  // way to exercise this specific render path end to end.
  await page.route('**/api/containers/maintenance', async (route) => {
    if (route.request().method() !== 'GET') {
      await route.continue();
      return;
    }
    const id = 'a'.repeat(64);
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        containers: [
          { id, full_id: id, name: 'always-on', image: 'demo/always-on:latest', state: 'exited', created: 1000, finished_at: 2000, restart_policy: 'always' },
        ],
        summary: { exited: 1, created: 0, dead: 0 },
      }),
    });
  });

  await page.goto('#/maintenance');
  const row = page.locator('.maintenance-table tr', { hasText: 'always-on' });
  await expect(row).toBeVisible();
  await expect(row.locator('.maintenance-badge--warn')).toHaveText('restarts on boot');
  await expect(row.locator('.maintenance-badge')).toHaveCount(1); // no managed badge on this fixture
  await expect(row.locator('input[type="checkbox"]')).not.toBeChecked();
});

test('the Maintenance nav entry routes to the view', async ({ page }) => {
  await page.goto('#/containers');
  await page.locator('a[href="#/maintenance"]').first().click();
  await expect(page).toHaveURL(/#\/maintenance$/);
  await expect(page.getByRole('heading', { name: 'Maintenance' })).toBeVisible();
});
