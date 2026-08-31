import { describe, expect, it } from 'vitest';
import { eventHref } from './eventHref';

describe('eventHref', () => {
  it('links container.* and docker.* to the entity\'s own container detail page', () => {
    expect(eventHref('container.start', 'jellyfin')).toBe('#/containers/jellyfin');
    expect(eventHref('container.die', 'jellyfin')).toBe('#/containers/jellyfin');
    expect(eventHref('container.oom', 'minecraft')).toBe('#/containers/minecraft');
    expect(eventHref('container.health', 'sonarr')).toBe('#/containers/sonarr');
    expect(eventHref('docker.something', 'gitea')).toBe('#/containers/gitea');
  });

  it('encodes an entity name that needs it', () => {
    expect(eventHref('container.start', 'my container')).toBe('#/containers/my%20container');
  });

  it('is null for a container/docker kind with no entity to link to', () => {
    expect(eventHref('container.start', '')).toBeNull();
  });

  it('links disk./array./parity./mover. to the plain Storage page', () => {
    expect(eventHref('disk.errors', 'disk2')).toBe('#/storage');
    expect(eventHref('array.state', 'array')).toBe('#/storage');
    expect(eventHref('parity.start', 'array')).toBe('#/storage');
    expect(eventHref('parity.finish', 'array')).toBe('#/storage');
    expect(eventHref('mover.start', 'array')).toBe('#/storage');
  });

  it('links alert.* to the Alerts view, even when the entity is a container/disk name that would otherwise misroute', () => {
    expect(eventHref('alert.fired', 'disk4')).toBe('#/alerts');
    expect(eventHref('alert.resolved', 'sonarr')).toBe('#/alerts');
    expect(eventHref('alert.fired', '')).toBe('#/alerts');
  });

  it('links insight.* to the Insights view, even when the entity is a container name that would otherwise misroute', () => {
    expect(eventHref('insight.detected', 'jellyfin')).toBe('#/insights');
    expect(eventHref('insight.resolved', 'sonarr')).toBe('#/insights');
    expect(eventHref('insight.detected', '')).toBe('#/insights');
  });

  it('routes a BARE victim_kind word (no dot) correctly too -- the Insights view/ImpactPanel call this with insight_instances.victim_kind directly, not a dot-namespaced event kind', () => {
    expect(eventHref('container', 'jellyfin')).toBe('#/containers/jellyfin');
    expect(eventHref('disk', 'disk3')).toBe('#/storage');
    expect(eventHref('array', '')).toBe('#/storage');
    expect(eventHref('host', '')).toBeNull();
    expect(eventHref('gpu', 'video')).toBeNull();
  });

  it('is null for image.* -- plain row for now, the images view does not exist yet', () => {
    expect(eventHref('image.pull', 'demo/jellyfin:latest')).toBeNull();
  });

  it('is null for an unrecognized kind', () => {
    expect(eventHref('gantry.something', '')).toBeNull();
    expect(eventHref('', '')).toBeNull();
  });
});
