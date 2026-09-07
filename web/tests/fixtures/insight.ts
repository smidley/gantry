import { test as base, expect } from '@playwright/test';
import { mockLiveStream } from './liveStream';

type Scenario = ReturnType<typeof makeScenario>;
function makeScenario(frame: any) {
  frame.host = { 'cpu.total': 26.4, 'mem.used_pct': 62.1, 'mem.used_bytes': 20e9, 'net.eth0.rx_bps': 4200000, 'net.eth0.tx_bps': 860000, 'diskio.sdb.read_bps': 12e6, 'diskio.sdb.write_bps': 3e6, ...frame.host };
  frame.disks = { disk1: { 'fs.used_bytes': 4e12, 'fs.free_bytes': 4e12, 'temp.c': 38, errors: 0 }, cache: { 'fs.used_bytes': 3e11, 'fs.free_bytes': 7e11, 'temp.c': 42, errors: 0 }, ...frame.disks };
  frame.disk_meta = { disk1: { device: 'sdb', kind: 'hdd' }, cache: { device: 'nvme0n1', kind: 'nvme' }, ...frame.disk_meta };
  frame.unraid = { ...frame.unraid, array: { 'array.started': 1, 'mover.running': 0, ...frame.unraid?.array } };
  const now = Math.floor(Date.now() / 1000);
  const recorded = (kind: string, entity: string, metric: string) => ({ kind, entity, metric, points: [[now-120, 20], [now-60, 80], [now-1, 45]] });
  const evidence = { attribution_version: 2, culprit_share_pct: 70, device_util_pct: 96, await_ms: 42, victim_stall_pct: 30, window_minutes: 2, other_users: ['plex'], iowait_pct: 24, host_cpu_pct: 96, spin_count: 0, spin_window_minutes: 0, engine_busy_pct: 0, baseline_pct: 0,
    recorded_series: [recorded('container','qbittorrent','cpu.pct'), recorded('container','jellyfin','cpu.throttled_pct'), recorded('container','qbittorrent','io.read_bps'), recorded('container','qbittorrent','io.write_bps'), recorded('host','','diskio.sdb.util_pct')], resource_device: 'sdb' };
  const common = { victim_kind: 'container', culprit: 'qbittorrent', culprits: '', severity: 'warning', confidence: 'likely', tier: 'proxy', started_at: now-120, fired_at: now-120, notified_at: 0, evidence };
  const items: any[] = [
    { ...common, id: 91001, rule_id: 'cpu-starvation', victim: 'jellyfin', resource: 'cpu', state: 'active', resolved_at: 0, resolve_reason: '', statement: 'qbittorrent is likely slowing jellyfin’s CPU while using 70% of host CPU.' },
    { ...common, id: 91002, rule_id: 'disk-io-contention', victim: 'plex', resource: 'disk1', state: 'resolved', resolved_at: now-1, resolve_reason: 'cleared', statement: 'qbittorrent is likely slowing plex on disk1, which is 96% busy.' },
  ];
  const active = () => items.filter((i) => i.state === 'active');
  const block = () => ({ active: active(), tier: 'proxy', suppressed: 0 });
  const snapshot = () => ({ ...frame, ts: Math.floor(Date.now()/1000), insights: block() });
  return {
    clearActive() { for (const item of items) item.state = 'resolved'; },
    activateAll() { items[1].state = 'active'; items[1].confidence = 'confirmed'; },
    response(rawURL: string, method = 'GET'): { status: number; body: any; sse?: boolean } | null {
      const url = new URL(rawURL);
      if (url.pathname === '/api/live') return { status: 200, body: `retry: 200\nevent: frame\ndata: ${JSON.stringify(snapshot())}\n\n`, sse: true };
      if (url.pathname === '/api/live/snapshot') return { status: 200, body: snapshot() };
      if (url.pathname === '/api/insights') return { status: 200, body: block() };
      if (url.pathname === '/api/insights/graph') {
        const nodes = new Map(); const edges = [];
        for (const item of active()) {
          const resource = `resource:${item.resource}`;
          for (const [id, kind, label] of [[item.culprit, 'container', item.culprit], [resource, 'resource', item.resource], [item.victim, 'container', item.victim]]) nodes.set(id, { id, kind, label });
          for (const [kind, from, to] of [['culprit', item.culprit, resource], ['victim', resource, item.victim]]) edges.push({ id: `${item.id}:${kind}`, from, to, kind, insight_id: item.id, rule_id: item.rule_id, confidence: item.confidence, severity: item.severity, share_pct: item.evidence.culprit_share_pct });
        }
        return { status: 200, body: { nodes: [...nodes.values()], edges } };
      }
      if (url.pathname === '/api/insights/history') return { status: 200, body: items.filter((i) => i.state === 'resolved' && i.resolved_at >= Number(url.searchParams.get('from') || 0)) };
      const match = url.pathname.match(/^\/api\/insights\/(\d+)(\/dismiss)?$/);
      if (match) {
        const item = items.find((i) => i.id === Number(match[1]));
        if (!item) return { status: 404, body: { error: 'insight not found' } };
        if (method === 'POST' && match[2]) Object.assign(item, { state: 'resolved', resolved_at: Math.floor(Date.now()/1000), resolve_reason: 'dismissed' });
        return { status: 200, body: item };
      }
      // Deliberately empty history proves the saved evidence fallback works.
      if (url.pathname === '/api/series') return { status: 200, body: [] };
      return null;
    },
  };
}
export const test = base.extend<{ scenario: Scenario }>({
  scenario: async ({ baseURL }, use) => {
    const frame = await (await fetch(`${baseURL}/api/live/snapshot`)).json();
    await use(makeScenario(frame));
  },
  page: async ({ page, scenario }, use) => {
    await mockLiveStream(page);
    await page.route('**/api/**', async (route) => {
      const response = scenario.response(route.request().url(), route.request().method());
      if (!response) return route.continue();
      await route.fulfill({ status: response.status, contentType: response.sse ? 'text/event-stream' : 'application/json', body: response.sse ? response.body : JSON.stringify(response.body) });
    });
    await use(page);
  },
  request: async ({ request, scenario }, use) => {
    await use(new Proxy(request, { get(target, property) {
      if (property === 'get') return async (url: string, options: any) => {
        const response = scenario.response(url);
        return response ? { json: async () => response.body, status: () => response.status } : target.get(url, options);
      };
      const value = Reflect.get(target, property); return typeof value === 'function' ? value.bind(target) : value;
    } }));
  },
});
export { expect };
