// Sidebar structural tests via svelte/server (SSR string render, the
// CalloutRow.test.ts convention). serverName comes off the live.frame
// singleton (sse.svelte.ts) rather than a prop, so each test sets it
// directly before rendering -- the same value this store carries once
// an SSE frame arrives -- and resets it after so tests stay isolated.
import { afterEach, describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import Sidebar from './Sidebar.svelte';
import { live } from '../lib/sse.svelte';
import type { SnapshotDTO } from '../lib/api';

afterEach(() => {
  live.frame = null;
});

function renderSidebar(): string {
  return render(Sidebar).body;
}

describe('Sidebar', () => {
  it('renders the server name prominently once a live frame carries one', () => {
    live.frame = { server_name: 'tower' } as SnapshotDTO;
    const body = renderSidebar();
    expect(body).toContain('sidebar__server-name');
    expect(body).toContain('>tower<');
    expect(body).not.toContain('Container observability');
  });

  it('falls back to the plain tagline, with no empty element, when server_name is empty', () => {
    live.frame = { server_name: '' } as SnapshotDTO;
    const body = renderSidebar();
    expect(body).toContain('Container observability');
    expect(body).not.toContain('sidebar__server-name');
  });

  it('falls back to the plain tagline before any live frame has arrived', () => {
    live.frame = null;
    const body = renderSidebar();
    expect(body).toContain('Container observability');
    expect(body).not.toContain('sidebar__server-name');
  });
});
