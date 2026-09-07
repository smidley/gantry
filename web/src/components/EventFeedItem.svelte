<script>
  import HealthDot from './HealthDot.svelte';
  import { fmtRelTime } from '../lib/format';
  import { eventLabel } from '../lib/eventLabels';
  import { eventHref } from '../lib/eventHref';

  let { event, showAbsoluteTime = false } = $props();

  // The store's actual severity vocabulary is info/warning/alert (see
  // internal/store/events.go and every AppendEvent call site) -- NOT
  // the 4-slot status-color names. "alert" (oom, disk errors, ...) is
  // the most severe value in practice, so it maps to critical.
  const SEVERITY_STATUS = {
    info: 'good',
    warning: 'warning',
    alert: 'critical',
  };

  let href = $derived(eventHref(event.Kind, event.Entity));
</script>

<svelte:element this={href ? 'a' : 'div'} {href} class="event-feed-item" class:event-feed-item--link={!!href}>
  <HealthDot status={SEVERITY_STATUS[event.Severity] ?? 'good'} />
  <div class="event-feed-item__body">
    <div class="event-feed-item__head">
      <span class="event-feed-item__kind" title={event.Kind}>{eventLabel(event.Kind)}</span>
      {#if event.Entity}<span class="event-feed-item__entity">{event.Entity}</span>{/if}
      <span class="microlabel event-feed-item__time">{fmtRelTime(event.TS)}</span>
    </div>
    {#if showAbsoluteTime}
      <div class="microlabel event-feed-item__time-abs">{new Date(event.TS * 1000).toLocaleString()}</div>
    {/if}
    {#if event.Detail}
      <div class="event-feed-item__detail">{event.Detail}</div>
    {/if}
  </div>
</svelte:element>

<style>
  .event-feed-item {
    display: flex;
    gap: 0.5rem;
    padding: 0.5rem 0;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 8%, transparent);
  }
  /* href (clickable events): plain-anchor reset, same reason/rule as
     StatTile's own .stat-tile--link -- the row's children already carry
     their own colors. */
  .event-feed-item--link {
    color: inherit;
    text-decoration: none;
    cursor: pointer;
  }
  .event-feed-item--link:hover .event-feed-item__kind {
    text-decoration: underline;
  }
  .event-feed-item__body {
    flex: 1;
    min-width: 0;
  }
  .event-feed-item__head {
    display: flex;
    align-items: baseline;
    flex-wrap: wrap;
    gap: 0.5rem;
  }
  .event-feed-item__kind {
    font-weight: 500;
    color: var(--ink);
  }
  .event-feed-item__entity {
    color: var(--ink-2);
    font-size: 0.85rem;
  }
  .event-feed-item__time {
    margin-left: auto;
    white-space: nowrap;
  }
  .event-feed-item__time-abs {
    text-transform: none;
    letter-spacing: normal;
    margin-top: 0.15rem;
  }
  /* overflow-wrap: a container/image removal's own Detail carries a bare
     64-hex-character id (docker's own full id has no natural break point
     -- no spaces, hyphens, or word boundaries at all) -- reproduced live
     once container.removed/image.removed events existed: the same
     narrow-viewport overflow this component's own showAbsoluteTime fix
     already hit once, for the same underlying reason (one unbroken run
     of characters longer than the card is wide), just a different field. */
  .event-feed-item__detail {
    color: var(--ink-2);
    font-size: 0.85rem;
    margin-top: 0.15rem;
    overflow-wrap: anywhere;
  }
</style>
