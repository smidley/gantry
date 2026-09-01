import { defineConfig, devices } from '@playwright/test';

// Playwright smoke suite: drives the REAL binary (built with -tags
// webdist, same as `make release`/the Dockerfile), not vite's dev
// server -- these tests exercise the actual embedded SPA + Go API
// together, the same artifact that ships. GANTRY_FAKE_DATA=1 synthesizes
// a demo fleet (see internal/fake/fake.go) so the suite needs no real
// docker/unraid host under it; GANTRY_PORT=8391 keeps it off gantry's
// own default 8380 (and any dev instance a contributor might have
// running locally); GANTRY_DB_PATH points at a fresh mktemp'd sqlite
// file per run so the suite never touches a real config/gantry.db.
//
// webServer.command runs from this file's own directory (web/) --
// `cd ..` first so `make release` (and the ./gantry it produces) run
// from the repo root, matching how every other Makefile target expects
// to be invoked.
// 8401 rather than the 8391 this suite used on the parent branch: a
// PARALLEL WORKTREE of this repo runs the same suite on its own branch,
// and with reuseExistingServer two suites sharing one port silently
// adopt each other's half-matching servers (observed live: this
// branch's auth specs failing against the sibling's pre-auth binary,
// and both suites mutating one shared fake fleet). Each branch's suite
// gets its own port block; tests/auth.spec.ts uses PORT+1/PORT+2.
const PORT = 8401;
const BASE_URL = `http://127.0.0.1:${PORT}`;

export default defineConfig({
  testDir: './tests',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: 'list',
  use: {
    baseURL: BASE_URL,
    trace: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: {
    // GANTRY_AUTH=none: auth is mandatory now, so the shared instance
    // explicitly opts open -- every non-auth spec keeps its zero-friction
    // world (no setup or login screen in the way). tests/auth.spec.ts
    // boots its OWN instances to exercise the real setup/login flow.
    command: `sh -c "cd .. && make release >/dev/null && GANTRY_FAKE_DATA=1 GANTRY_AUTH=none GANTRY_DB_PATH=$(mktemp -d)/g.db GANTRY_PORT=${PORT} ./gantry"`,
    url: `${BASE_URL}/api/healthz`,
    // make release (npm ci + vite build + go build) comfortably clears
    // this from a cold cache; reuseExistingServer keeps local iteration
    // fast against an already-running instance on this port while still
    // forcing a fresh build+run on CI every time.
    timeout: 120_000,
    reuseExistingServer: !process.env.CI,
  },
});
