import { defineConfig, devices } from '@playwright/test';
import { freePort } from './tests/fixtures/freePort';

// Each run owns a fresh server and database. An explicit port is available
// for debugging; independent worktrees no longer adopt a stale server.
// The fake fleet has no access to the host Docker socket, so unrelated
// local containers cannot enter ranking or history assertions.
const PORT = process.env.GANTRY_TEST_PORT ? Number(process.env.GANTRY_TEST_PORT) : await freePort();
if (!Number.isInteger(PORT) || PORT < 1 || PORT > 65535) throw new Error('Invalid GANTRY_TEST_PORT');
process.env.GANTRY_TEST_PORT = String(PORT);
const BASE_URL = `http://127.0.0.1:${PORT}`;
export default defineConfig({
  testDir: './tests',
  fullyParallel: true,
  workers: process.env.CI ? 2 : 4,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: 'list',
  use: { baseURL: BASE_URL, trace: 'retain-on-failure' },
  projects: [
    { name: 'chromium', testIgnore: /insights-pipeline/, use: { ...devices['Desktop Chrome'] } },
    { name: 'firefox', testMatch: /accessibility.spec.ts/, use: { ...devices['Desktop Firefox'] } },
    ...(process.env.GANTRY_PIPELINE ? [{ name: 'pipeline', testMatch: /insights-pipeline.spec.ts/, use: { ...devices['Desktop Chrome'] } }] : []),
  ],
  webServer: {
    command: `sh -c 'npm run build >/dev/null && cd .. && CGO_ENABLED=0 go build -trimpath -tags webdist -o gantry ./cmd/gantry && GANTRY_BIND_ADDRESS=127.0.0.1 GANTRY_DOCKER_SOCK=/tmp/gantry-e2e-${PORT}.sock GANTRY_FAKE_DATA=1 GANTRY_AUTH=none GANTRY_DB_PATH=$(mktemp -d)/g.db GANTRY_PORT=${PORT} ./gantry'`,
    url: `${BASE_URL}/api/healthz`,
    timeout: 120_000,
    reuseExistingServer: false,
  },
});
