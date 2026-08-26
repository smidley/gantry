<!--
  EventFeedItem: one row for the Overview/Events feeds. `event` is a
  GantryEvent (api.ts) -- store.Event's wire shape has no json tags, so
  its capitalized Go field names (ID, TS, Kind, Entity, Severity,
  Detail) are the JSON/prop keys as-is.
-->
<script>
  import HealthDot from './HealthDot.svelte';
  import { fmtRelTime } from '../lib/format';

  let { event } = $props();

  // The store's actual severity vocabulary is info/warning/alert (see
  // internal/store/events.go and every AppendEvent call site) -- NOT
  // the 4-slot status-color names. "alert" (oom, disk errors, ...) is
  // the most severe value in practice, so it maps to critical.
  const SEVERITY_STATUS = {
    info: 'good',
    warning: 'warning',
    alert: 'critical',
  };
</script>

<div class="event-feed-item">
  <HealthDot status={SEVERITY_STATUS[event.Severity] ?? 'good'} />
  <div class="event-feed-item__body">
    <div class="event-feed-item__head">
      <span class="event-feed-item__kind">{event.Kind}</span>
      {#if event.Entity}<span class="event-feed-item__entity">{event.Entity}</span>{/if}
      <span class="microlabel event-feed-item__time">{fmtRelTime(event.TS)}</span>
    </div>
    {#if event.Detail}
      <div class="event-feed-item__detail">{event.Detail}</div>
    {/if}
  </div>
</div>

<style>
  .event-feed-item {
    display: flex;
    gap: 0.5rem;
    padding: 0.5rem 0;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 8%, transparent);
  }
  .event-feed-item__body {
    flex: 1;
    min-width: 0;
  }
  .event-feed-item__head {
    display: flex;
    align-items: baseline;
    gap: 0.5rem;
  }
  .event-feed-item__kind {
    font-family: var(--font-mono);
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
  .event-feed-item__detail {
    color: var(--ink-2);
    font-size: 0.85rem;
    margin-top: 0.15rem;
  }
</style>
