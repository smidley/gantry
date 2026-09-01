import { expect, test } from '@playwright/test';
import { type ChildProcess, spawn } from 'node:child_process';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';

// Auth flow, end to end against the real binary. Auth is mandatory now,
// so the shared webServer instance (playwright.config.ts) runs
// GANTRY_AUTH=none to keep every other spec's zero-friction world; this
// file boots its own gantry processes -- the same ./gantry artifact
// `make release` already produced -- on their own ports and databases:
//
//   LOGIN (8402): GANTRY_USERNAME + GANTRY_PASSWORD preseeded, the
//   headless/CA path. Drives username+password login (wrong password,
//   deep link, reload, the session cookie), logout, the 401-redirect,
//   and the brute-force limiter's UI surface.
//
//   SETUP (8403): boots with NO credential -- the first-run setup screen,
//   then the Settings access card's change-login flow.
//
// Serial: these tests share process state on purpose (the limiter's
// token bucket most of all -- see the rate-limit test's own doc).
test.describe.configure({ mode: 'serial' });

const USERNAME = 'crane-admin';
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

test.describe('login gate (preseeded instance)', () => {
  const PORT = 8402; // the suite's own block: config PORT+1 -- see playwright.config.ts
  const URL = `http://127.0.0.1:${PORT}`;
  let proc: ChildProcess;

  test.beforeAll(async () => {
    proc = startGantry(PORT, { GANTRY_USERNAME: USERNAME, GANTRY_PASSWORD: PASSWORD });
    await waitForHealthz(PORT);
  });
  test.afterAll(() => {
    proc?.kill('SIGTERM');
  });

  test('a preseeded box shows the login screen with a username field, never the dashboard shell', async ({ page }) => {
    await page.goto(`${URL}/#/containers`);
    await expect(page.locator('.login__card')).toBeVisible();
    await expect(page.locator('input[autocomplete="username"]')).toBeVisible();
    await expect(page.locator('input[autocomplete="username"]')).toBeFocused();
    await expect(page.locator('input[autocomplete="current-password"]')).toBeVisible();
    await expect(page.locator('.sidebar')).toHaveCount(0);

    // And the API behind it really is closed -- this isn't a cosmetic
    // overlay.
    const res = await page.request.get(`${URL}/api/live/snapshot`);
    expect(res.status()).toBe(401);
  });

  test('wrong password errors inline; the right username+password land on the deep link and the cookie is a session cookie', async ({ page }) => {
    await page.goto(`${URL}/#/containers`);
    await page.locator('input[autocomplete="username"]').fill(USERNAME);
    await page.locator('input[autocomplete="current-password"]').fill('not-the-password');
    await page.getByRole('button', { name: 'Unlock' }).click();
    await expect(page.locator('.login__error')).toHaveText('Wrong username or password. Try again.');

    await page.locator('input[autocomplete="username"]').fill(USERNAME);
    await page.locator('input[autocomplete="current-password"]').fill(PASSWORD);
    await page.getByRole('button', { name: 'Unlock' }).click();
    // The hash survived the round-trip: we land on Containers, not
    // Overview, with the fake fleet actually streaming.
    await expect(page.locator('.sidebar')).toBeVisible();
    await expect(page).toHaveURL(`${URL}/#/containers`);
    await expect(page.getByText('jellyfin').first()).toBeVisible();

    // "Until browser closes": the session cookie carries no expiry, so
    // Playwright reports expires === -1.
    const cookies = await page.context().cookies();
    const sess = cookies.find((c) => c.name === 'gantry_session');
    expect(sess, 'a session cookie must be set').toBeTruthy();
    expect(sess!.expires, 'the session cookie must have no expiry (until browser closes)').toBe(-1);

    // The cookie session survives a full reload -- no re-login.
    await page.reload();
    await expect(page.locator('.sidebar')).toBeVisible();
    await expect(page.locator('.login__card')).toHaveCount(0);
  });

  test('logout from Settings returns to the login screen', async ({ page }) => {
    await page.goto(`${URL}/#/settings`);
    await page.locator('input[autocomplete="username"]').fill(USERNAME);
    await page.locator('input[autocomplete="current-password"]').fill(PASSWORD);
    await page.getByRole('button', { name: 'Unlock' }).click();
    await expect(page.getByRole('button', { name: 'Log out' })).toBeVisible();

    await page.getByRole('button', { name: 'Log out' }).click();
    await expect(page.locator('.login__card')).toBeVisible();

    // The server-side session is gone too, not just the tab state.
    const res = await page.request.get(`${URL}/api/live/snapshot`);
    expect(res.status()).toBe(401);
  });

  test('a 401 mid-session bounces to the login screen (expired/revoked session)', async ({ page, context }) => {
    await page.goto(`${URL}/#/`);
    await page.locator('input[autocomplete="username"]').fill(USERNAME);
    await page.locator('input[autocomplete="current-password"]').fill(PASSWORD);
    await page.getByRole('button', { name: 'Unlock' }).click();
    await expect(page.locator('.sidebar')).toBeVisible();

    // Simulate expiry/revocation: drop the cookie, then steer to a view
    // that fetches on mount. The hash is set directly rather than clicking
    // the sidebar link: some surface usually 401s on its own within a
    // frame or two and App swaps to the login screen mid-click.
    await context.clearCookies();
    await page.evaluate(() => {
      window.location.hash = '#/events';
    });
    await expect(page.locator('.login__card')).toBeVisible();
  });

  test('the login limiter surfaces its wait message', async ({ page }) => {
    // Attempt budget arithmetic for this serial file: the per-IP bucket
    // holds 5 tokens refilling one per 12s. Earlier tests spent some, but
    // a slow CI box could have refilled them -- so hammer SIX wrong
    // attempts: even from a completely full bucket, the sixth is denied
    // and the limiter message is what's left showing.
    await page.goto(`${URL}/#/`);
    const user = page.locator('input[autocomplete="username"]');
    const pass = page.locator('input[autocomplete="current-password"]');
    for (let i = 0; i < 6; i++) {
      await user.fill(USERNAME);
      await pass.fill(`wrong-${i}`);
      await page.getByRole('button', { name: 'Unlock' }).click();
      await expect(page.locator('.login__error')).toBeVisible();
    }
    await expect(page.locator('.login__error')).toHaveText('Too many attempts. Wait a minute, then try again.');
  });
});

test.describe('first-run setup (unconfigured instance)', () => {
  const PORT = 8403; // config PORT+2
  const URL = `http://127.0.0.1:${PORT}`;
  let proc: ChildProcess;

  test.beforeAll(async () => {
    proc = startGantry(PORT, {}); // no credential env: first-run setup
    await waitForHealthz(PORT);
  });
  test.afterAll(() => {
    proc?.kill('SIGTERM');
  });

  test('setup screen creates the credential, validates locally, and auto-signs-in', async ({ page }) => {
    // Unconfigured box: the setup screen, not the dashboard, and the API
    // is closed behind it.
    await page.goto(`${URL}/#/containers`);
    await expect(page.locator('.setup__card')).toBeVisible();
    await expect(page.locator('input[autocomplete="username"]')).toBeFocused();
    await expect(page.locator('.sidebar')).toHaveCount(0);
    const closed = await page.request.get(`${URL}/api/live/snapshot`);
    expect(closed.status()).toBe(401);

    const card = page.locator('.setup__card');
    const newPw = () => card.locator('input[autocomplete="new-password"]');

    // Mismatched confirm: the local check answers, no request needed.
    await card.locator('input[autocomplete="username"]').fill(USERNAME);
    await newPw().first().fill(PASSWORD);
    await newPw().nth(1).fill('something-else');
    await page.getByRole('button', { name: 'Create login' }).click();
    await expect(card.locator('.setup__error')).toHaveText("Passwords don't match.");

    // Too-short password: also local.
    await newPw().first().fill('short');
    await newPw().nth(1).fill('short');
    await page.getByRole('button', { name: 'Create login' }).click();
    await expect(card.locator('.setup__error')).toHaveText('Password must be at least 8 characters.');

    // Valid: creates the credential and drops straight into the app on the
    // deep-linked view, streaming the fake fleet -- no separate login.
    await newPw().first().fill(PASSWORD);
    await newPw().nth(1).fill(PASSWORD);
    await page.getByRole('button', { name: 'Create login' }).click();
    await expect(page.locator('.sidebar')).toBeVisible();
    await expect(page).toHaveURL(`${URL}/#/containers`);
    await expect(page.getByText('jellyfin').first()).toBeVisible();

    // A cookie-less client is now sent to the LOGIN screen (the credential
    // exists), never back to setup.
    const anon = await page.context().browser()!.newContext();
    const anonPage = await anon.newPage();
    await anonPage.goto(`${URL}/#/`);
    await expect(anonPage.locator('.login__card')).toBeVisible();
    await expect(anonPage.locator('.setup__card')).toHaveCount(0);
    await anon.close();
  });

  test('the Settings access card changes the username and password', async ({ page }) => {
    // Fresh context: log in with the credential the previous test created.
    await page.goto(`${URL}/#/settings`);
    await page.locator('input[autocomplete="username"]').fill(USERNAME);
    await page.locator('input[autocomplete="current-password"]').fill(PASSWORD);
    await page.getByRole('button', { name: 'Unlock' }).click();

    const card = page.locator('.settings-access');
    await expect(card).toContainText(`Signed in as ${USERNAME}`);
    await expect(card).toContainText('Signing in lasts until you close your browser');

    // Change both the username and the password. Current password is
    // required; the username field is prefilled with the current one.
    await card.locator('input[autocomplete="current-password"]').fill(PASSWORD);
    await card.locator('input[autocomplete="username"]').fill('crane-admin-2');
    await card.locator('input[autocomplete="new-password"]').first().fill('brand-new-password-2');
    await card.locator('input[autocomplete="new-password"]').nth(1).fill('brand-new-password-2');
    await page.getByRole('button', { name: 'Update login' }).click();

    await expect(card.locator('.settings-access__success')).toHaveText('Login updated. Every other session was signed out.');
    // The card reflects the new username after the store refreshes.
    await expect(card).toContainText('Signed in as crane-admin-2');
  });
});

test.describe('auth-off default (shared instance)', () => {
  // The suite's own webServer: GANTRY_AUTH=none -- the existing specs all
  // run against it untouched, and this pins the two visible guarantees of
  // the explicit-open mode.
  test('boots straight to the dashboard, and Settings says authentication is off', async ({ page }) => {
    await page.goto('/#/settings');
    await expect(page.locator('.sidebar')).toBeVisible();
    await expect(page.locator('.login__card')).toHaveCount(0);
    await expect(page.locator('.setup__card')).toHaveCount(0);
    await expect(page.locator('.settings-access')).toContainText('Authentication is turned off');
  });
});
