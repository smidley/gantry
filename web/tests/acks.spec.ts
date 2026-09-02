import { test, expect } from '@playwright/test';

// Overview acknowledgements -- the /api/acks round trip, driven against
// the real fake-mode binary (playwright.config.ts's webServer) through
// Playwright's request fixture (baseURL comes from the config).
//
// The API round trip stays pinned at this level whatever Overview
// renders: the wire contract (shapes, bounds, idempotent DELETE) is the
// server's own promise, independent of any one consumer. The UI half
// moved to tests/overview-attention.spec.ts's own last spec -- the
// counts pass replaced Overview's per-item rows (and their Ack control)
// with count chips, so what an ack visibly does now is drop the COUNT,
// which is what that spec asserts. The derivation/expiry logic behind
// it is unit-tested in src/lib/overviewStatus.test.ts and
// src/components/CalloutRow.test.ts.
//
// The round-trip test cleans up after itself (reuseExistingServer means
// a local dev server outlives any one run), and its probe entity names a
// container no fleet -- fake or real -- would ever have, so it can never
// collide with another spec's expectations. The server deliberately does
// NOT validate entity existence (the silences precedent: rule_id/entity
// are taken as given), which is also what makes a synthetic probe safe.

test('ack round-trip: POST creates with the requested window, GET lists it, DELETE lifts it', async ({ request }) => {
  const probeEntity = 'gantry-e2e-ack-probe';

  const created = await request.post('/api/acks', {
    headers: { 'X-Requested-With': 'gantry' },
    data: { kind: 'unhealthy', entity: probeEntity, hours: 1 },
  });
  expect(created.ok()).toBe(true);
  const ack = await created.json();
  expect(ack.id).toBeGreaterThan(0);
  expect(ack.kind).toBe('unhealthy');
  expect(ack.entity).toBe(probeEntity);
  // until = created_at + exactly the requested hour, both server-stamped.
  expect(ack.until - ack.created_at).toBe(3600);

  try {
    const listed = await (await request.get('/api/acks')).json();
    expect(listed.acks.some((a: { id: number }) => a.id === ack.id)).toBe(true);
  } finally {
    const deleted = await request.delete(`/api/acks/${ack.id}`, { headers: { 'X-Requested-With': 'gantry' } });
    expect(deleted.status()).toBe(204);
  }

  const after = await (await request.get('/api/acks')).json();
  expect(after.acks.some((a: { id: number }) => a.id === ack.id)).toBe(false);

  // Lifting an already-lifted ack is idempotent (204, never an error).
  expect((await request.delete(`/api/acks/${ack.id}`, { headers: { 'X-Requested-With': 'gantry' } })).status()).toBe(204);
});

test('POST /api/acks rejects every shape that must not exist', async ({ request }) => {
  const rejected = [
    // 'alert' is not an ackable kind: acknowledging an alert-backed
    // callout IS an alert silence (one mechanism per system).
    { kind: 'alert', entity: 'sonarr', hours: 1 },
    // 'stopped' is no longer an anomaly kind at all.
    { kind: 'stopped', entity: 'sonarr', hours: 1 },
    // No global ack shape: entity is required, and kind+entity both
    // blank has no scope:"all" escape hatch the way silences do.
    { kind: 'unhealthy', entity: '', hours: 1 },
    { kind: '', entity: '', hours: 1 },
    // The 1h-7d preset range is the wire contract too.
    { kind: 'unhealthy', entity: 'sonarr', hours: 0 },
    { kind: 'unhealthy', entity: 'sonarr', hours: 169 },
  ];
  for (const body of rejected) {
    const resp = await request.post('/api/acks', { headers: { 'X-Requested-With': 'gantry' }, data: body });
    expect(resp.status(), JSON.stringify(body)).toBe(400);
  }

  // And none of those rejections may have created anything.
  const listed = await (await request.get('/api/acks')).json();
  expect(listed.acks.some((a: { entity: string }) => a.entity === 'sonarr')).toBe(false);
});

