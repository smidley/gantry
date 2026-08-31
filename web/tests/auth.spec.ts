import { expect, test } from '@playwright/test';
import { type ChildProcess, spawn } from 'node:child_process';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';

// Auth flow, end to end against the real binary. The shared webServer
// instance (playwright.config.ts) deliberately stays auth-off so every
// other spec keeps its zero-config world; this file boots its own
// gantry processes -- the same ./gantry artifact `make release` already
// produced for the webServer -- on their own ports and databases:
//
//   LOCKED (8392): GANTRY_PASSWORD set at boot, the template-variable
//   path. Drives login (wrong password, deep link, reload), the
//   401-redirect, logout, and the brute-force limiter's UI surface.
//
//   SETUP (8393): boots open, then exercises the Settings access card's
//   whole lifecycle -- nudge, set, change-gated state, turn off.
//
// Serial: these tests share process state on purpose (the limiter's
// token bucket most of all -- see the rate-limit test's own doc for the
// attempt budget).
test.describe.configure({ mode: 'serial' });

const PASSWORD = 'e2e-orange-crane-9';

function startGantry(port: number, extraEnv: Record<string, string>): ChildProcess {
  const bin = path.resolve(process.cwd(), '..', 'gantry');
  return spawn(bin, [], {
    env: {
      ...process.env,
      GANTRY_PORT: String(port),
      GANTRY_DB_PATH: path.join(mkdtempSync(path.join(tmpdir(), 'gantry-auth-')), 'g.db'),
      GANTRY_FAKE_DATA: '1',
      ...extraEnv,
    },
    stdio: 'ignore',
  });
}

async function waitForHealthz(port: number): Promise<void> {
  const deadline = Date.now() + 15_000;
  for (;;) {
    try {
      const res = await fetch(`http://127.0.0.1:${port}/api/healthz`);
      if (res.status === 200) return;
    } catch {
      // not up yet
    }
    if (Date.now() > deadline) throw new Error(`gantry on :${port} never became healthy`);
    await new Promise((r) => setTimeout(r, 100));
  }
}

test.describe('password gate (locked instance)', () => {
  const PORT = 8402; // the suite's own block: config PORT+1 -- see playwright.config.ts
  const URL = `http://127.0.0.1:${PORT}`;
  let proc: ChildProcess;

  test.beforeAll(async () => {
    proc = startGantry(PORT, { GANTRY_PASSWORD: PASSWORD });
    await waitForHealthz(PORT);
  });
  test.afterAll(() => {
    proc?.kill('SIGTERM');
  });

  test('a locked box shows the login screen, never the dashboard shell', async ({ page }) => {
    await page.goto(`${URL}/#/containers`);
    await expect(page.locator('.login__card')).toBeVisible();
    await expect(page.locator('input[type="password"]')).toBeFocused();
    await expect(page.locator('.sidebar')).toHaveCount(0);

    // And the API behind it really is closed -- this isn't a cosmetic
    // overlay.
    const res = await page.request.get(`${URL}/api/live/snapshot`);
    expect(res.status()).toBe(401);
  });

  test('wrong password errors inline; the right one lands on the deep link and survives reload', async ({ page }) => {
    await page.goto(`${URL}/#/containers`);
    await page.locator('input[type="password"]').fill('not-the-password');
    await page.getByRole('button', { name: 'Unlock' }).click();
    await expect(page.locator('.login__error')).toHaveText('Wrong password. Try again.');

    await page.locator('input[type="password"]').fill(PASSWORD);
    await page.getByRole('button', { name: 'Unlock' }).click();
    // The hash survived the round-trip: we land on Containers, not
    // Overview, with the fake fleet actually streaming.
    await expect(page.locator('.sidebar')).toBeVisible();
    await expect(page).toHaveURL(`${URL}/#/containers`);
    await expect(page.getByText('jellyfin').first()).toBeVisible();

    // The cookie session survives a full reload -- no re-login.
    await page.reload();
    await expect(page.locator('.sidebar')).toBeVisible();
    await expect(page.locator('.login__card')).toHaveCount(0);
  });

  test('logout from Settings returns to the login screen', async ({ page }) => {
    // Each test gets a fresh context, so log in first, then out.
    await page.goto(`${URL}/#/settings`);
    await page.locator('input[type="password"]').fill(PASSWORD);
    await page.getByRole('button', { name: 'Unlock' }).click();
    await expect(page.getByRole('button', { name: 'Log out' })).toBeVisible();

    await page.getByRole('button', { name: 'Log out' }).click();
    await expect(page.locator('.login__card')).toBeVisible();

    // The server-side session is gone too, not just the tab state: the
    // cookie jar's token no longer opens the API.
    const res = await page.request.get(`${URL}/api/live/snapshot`);
    expect(res.status()).toBe(401);
  });

  test('a 401 mid-session bounces to the login screen (expired/revoked session)', async ({ page, context }) => {
    await page.goto(`${URL}/#/`);
    await page.locator('input[type="password"]').fill(PASSWORD);
    await page.getByRole('button', { name: 'Unlock' }).click();
    await expect(page.locator('.sidebar')).toBeVisible();

    // Simulate expiry/revocation: drop the cookie, then steer to a view
    // that fetches on mount. The hash is set directly rather than
    // clicking the sidebar link: some Overview surface usually 401s on
    // its own within a frame or two and App swaps to the login screen
    // mid-click, which turns a DOM click into a detached-element race
    // -- the assertion is the swap itself, whichever call loses first.
    await context.clearCookies();
    await page.evaluate(() => {
      window.location.hash = '#/events';
    });
    await expect(page.locator('.login__card')).toBeVisible();
  });

  test('the login limiter surfaces its wait message', async ({ page }) => {
    // Attempt budget arithmetic for this serial file: the per-IP bucket
    // holds 5 tokens refilling one per 12s. The tests above spent 4
    // (1 wrong + 3 successful logins), but a slow CI box could have
    // refilled all of that back -- so hammer SIX wrong attempts: even
    // from a completely full bucket, the sixth is denied and the
    // limiter message is what's left showing.
    await page.goto(`${URL}/#/`);
    const field = page.locator('input[type="password"]');
    for (let i = 0; i < 6; i++) {
      await field.fill(`wrong-${i}`);
      await page.getByRole('button', { name: 'Unlock' }).click();
      await expect(page.locator('.login__error')).toBeVisible();
    }
    await expect(page.locator('.login__error')).toHaveText('Too many attempts. Wait a minute, then try again.');
  });
});

test.describe('access card lifecycle (setup instance)', () => {
  const PORT = 8403; // config PORT+2
  const URL = `http://127.0.0.1:${PORT}`;
  let proc: ChildProcess;

  test.beforeAll(async () => {
    proc = startGantry(PORT, {});
    await waitForHealthz(PORT);
  });
  test.afterAll(() => {
    proc?.kill('SIGTERM');
  });

  test('nudge, set password, stay signed in, log back in, turn off', async ({ page }) => {
    // Open box: straight to the dashboard, and Settings carries the
    // quiet warning.
    await page.goto(`${URL}/#/settings`);
    await expect(page.locator('.sidebar')).toBeVisible();
    const nudge = page.locator('.settings-access__nudge');
    await expect(nudge).toHaveText(/No password set — anyone on your network can view and manage this server\./);

    // Set it. Mismatched confirm first -- the local check answers, no
    // request needed.
    const access = page.locator('.settings-access');
    await access.locator('input[autocomplete="new-password"]').first().fill(PASSWORD);
    await access.locator('input[autocomplete="new-password"]').nth(1).fill('something-else');
    await access.getByRole('button', { name: 'Set password' }).click();
    await expect(access.locator('.settings-access__error')).toHaveText("Passwords don't match.");

    await access.locator('input[autocomplete="new-password"]').nth(1).fill(PASSWORD);
    await access.getByRole('button', { name: 'Set password' }).click();
    await expect(access.locator('.settings-access__success')).toHaveText('Password set. This browser stays signed in.');
    await expect(nudge).toHaveCount(0);

    // This browser kept its session (the response carried a fresh
    // cookie); the dashboard still works without any login screen.
    await page.goto(`${URL}/#/`);
    await expect(page.locator('.sidebar')).toBeVisible();

    // But a cookie-less client is now locked out.
    const anon = await page.context().browser()!.newContext();
    const anonRes = await anon.request.get(`${URL}/api/live/snapshot`);
    expect(anonRes.status()).toBe(401);
    await anon.close();

    // Turn it back off through the armed flow (current password
    // required), and the box is open again.
    await page.goto(`${URL}/#/settings`);
    await page.getByRole('button', { name: 'Turn off password…' }).click();
    await page.locator('.settings-access__disable input[type="password"]').fill(PASSWORD);
    await page.getByRole('button', { name: 'Turn off password', exact: true }).click();
    await expect(page.locator('.settings-access__nudge')).toBeVisible();

    const openAgain = await page.context().browser()!.newContext();
    const openRes = await openAgain.request.get(`${URL}/api/live/snapshot`);
    expect(openRes.status()).toBe(200);
    await openAgain.close();
  });
});

test.describe('auth-off default (shared instance)', () => {
  // The suite's own webServer: zero-config, no password -- the existing
  // specs all run against it untouched, and this pins the two visible
  // guarantees of the open default.
  test('boots straight to the dashboard with the Settings nudge showing', async ({ page }) => {
    await page.goto('/#/settings');
    await expect(page.locator('.sidebar')).toBeVisible();
    await expect(page.locator('.login__card')).toHaveCount(0);
    await expect(page.locator('.settings-access__nudge')).toBeVisible();
  });
});
