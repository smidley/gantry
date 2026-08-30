import { test, expect } from '@playwright/test';

// Custom container groups (Scott's own ask: "make a way for a user to
// group certain containers together for easy compare") -- a named,
// user-picked set of container names, persisted server-side via
// GET/PUT /api/groups, distinct from composeGroups.ts's own
// docker-compose-derived groups. Every test below names its own group
// with a Date.now()-suffixed name: this suite shares one server/DB
// with every other spec file (playwright.config.ts's webServer), so a
// fixed name could collide with a concurrent run: -- and each test
// deletes what it created, so a repeated local run (reuseExistingServer)
// never accumulates stale groups.
//
// "jellyfin"/"plex"/"radarr" are fixed fake-fleet archetypes (internal/
// fake/fake.go), always present and always state=running -- see
// compare.spec.ts's own identical note.

test('containers -> compare -> save as group -> chip appears -> pre-fills compare -> rename -> delete', async ({ page }) => {
  const groupName = `E2E Group ${Date.now()}`;

  await page.goto('#/containers');
  const rowFor = (name: string) => page.locator('tr.container-row', { hasText: name });
  await rowFor('jellyfin').locator('.container-row__select').check();
  await rowFor('plex').locator('.container-row__select').check();
  await rowFor('radarr').locator('.container-row__select').check();

  const bar = page.locator('.containers-view__compare-bar');
  await expect(bar).toBeVisible();
  await bar.locator('.containers-view__compare-btn').click();
  await expect(page).toHaveURL(/#\/compare\/jellyfin,plex,radarr$/);

  // Save as group: the trigger link swaps for a name input + Save.
  const saveOpen = page.locator('.compare__save-group-open');
  await expect(saveOpen).toBeVisible();
  await saveOpen.click();

  const nameInput = page.locator('.compare__save-group-input');
  await nameInput.fill(groupName);
  await page.locator('.compare__save-group-btn').click();
  await expect(page.locator('.compare__save-group-success')).toHaveText('Saved.');
  await expect(page.locator('.compare__save-group-error')).toHaveCount(0);

  // Containers view: a bookmarked chip for the new group, distinguishable
  // from a plain compose chip by its own modifier class, pre-filling
  // compare with the exact member set it was saved with.
  await page.goto('#/containers');
  const chip = page.locator('.containers-view__group-chip--custom', { hasText: groupName });
  await expect(chip).toBeVisible();
  await expect(chip).toContainText('×3');
  const chipLink = chip.locator('.containers-view__group-chip-link');
  await expect(chipLink).toHaveAttribute('href', '#/compare/jellyfin,plex,radarr');

  await chipLink.click();
  await expect(page).toHaveURL(/#\/compare\/jellyfin,plex,radarr$/);
  await expect(page.locator('.compare__chip')).toHaveCount(3);
  await expect(page.locator('.compare__chip')).toContainText(['jellyfin', 'plex', 'radarr']);

  // Manage: rename via the chip's own ⋯ editor. Every locator below
  // matches its group's aria-label attribute EXACTLY (not a text
  // substring) -- renamedName itself contains groupName as a
  // substring, so a plain hasText match couldn't tell the two chips
  // apart, and this suite's tests may run concurrently against the
  // same shared server (playwright.config.ts's fullyParallel).
  await page.goto('#/containers');
  const renamedName = `${groupName} renamed`;
  await page.locator(`button[aria-label="Manage group ${groupName}"]`).click();

  const editForm = page.locator('.containers-view__group-chip--editing');
  await expect(editForm).toBeVisible();
  await editForm.locator('.containers-view__group-edit-input').fill(renamedName);
  await editForm.getByRole('button', { name: 'Save name' }).click();

  await expect(page.locator(`a[href="#/compare/jellyfin,plex,radarr"]`, { hasText: renamedName })).toBeVisible();
  await expect(page.locator(`button[aria-label="Manage group ${groupName}"]`)).toHaveCount(0);

  // Manage: delete -- the group chip disappears, but the containers it
  // named are completely unaffected (still real, running rows).
  await page.locator(`button[aria-label="Manage group ${renamedName}"]`).click();
  await page.locator(`button[aria-label="Delete group ${renamedName}"]`).click();

  await expect(page.locator(`button[aria-label="Manage group ${renamedName}"]`)).toHaveCount(0);
  await expect(rowFor('jellyfin')).toBeVisible();
  await expect(rowFor('plex')).toBeVisible();
  await expect(rowFor('radarr')).toBeVisible();
});

test('groups: saved groups survive a server restart (persisted, not just in-memory)', async ({ page, request, baseURL }) => {
  const groupName = `E2E Persist ${Date.now()}`;

  // Round-trips straight through the API -- the UI flow above already
  // covers the browser-driven path; this test's own job is persistence
  // across a process restart, which the running webServer can't
  // simulate mid-suite, so it instead pins the one thing that WOULD
  // break if groups were ever accidentally kept in memory only: a
  // fresh page load, well after the save, still sees it via a plain
  // GET -- no client-side cache path involved.
  const putResp = await request.put(`${baseURL}/api/groups`, {
    data: { groups: [{ name: groupName, members: ['jellyfin'] }] },
  });
  expect(putResp.ok()).toBe(true);

  await page.goto('#/containers');
  await expect(page.locator('.containers-view__group-chip--custom', { hasText: groupName })).toBeVisible();

  const getResp = await request.get(`${baseURL}/api/groups`);
  const body = await getResp.json();
  expect(body.groups.some((g: { name: string }) => g.name === groupName)).toBe(true);

  // Clean up -- read the current list back and drop just this one
  // entry, so a concurrent test's own group (a different Date.now()
  // suffix) is never clobbered by a bare {"groups":[]} overwrite.
  const current = (await (await request.get(`${baseURL}/api/groups`)).json()).groups as { name: string; members: string[] }[];
  await request.put(`${baseURL}/api/groups`, {
    data: { groups: current.filter((g) => g.name !== groupName) },
  });
});
