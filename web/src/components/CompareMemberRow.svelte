<!--
  CompareMemberRow: one <tr> in the compare view's per-member detail
  table -- its own component (same reason as ContainerRow: a per-name
  Tween needs to live in a component instantiated once per member, kept
  stable by the caller's own keyed {#each}, not declared inline in a
  loop). Always LIVE (current-instant), independent of the compare page's
  own chart range picker -- same split ContainerDetail's header facts
  already have against its charts: "how is this member doing right now,"
  not a historical reading.

  mem shows percent of the container's OWN memory limit when it has one
  (mem.limit_pct/mem.limit_bytes), not host-share -- deliberately
  different from ContainerRow's own mem cell (host-share mem.pct): the
  compare page's group TOTALS row already covers "how much of the HOST"
  this team is using; this row's own job is "how is each member doing
  relative to ITS OWN ceiling," which host-share can't answer for an
  unlimited container anyway.
-->
<script>
  import { Tween } from 'svelte/motion';
  import { linear } from 'svelte/easing';
  import { motion } from '../lib/motion.svelte';
  import { live } from '../lib/sse.svelte';
  import { fmtBytes, fmtCores, fmtDuration, fmtPct, fmtRate } from '../lib/format';
  import { containerHealthStatus } from '../lib/containerStatus';
  import ContainerIcon from './ContainerIcon.svelte';
  import HealthDot from './HealthDot.svelte';

  // colorVar: this member's own assigned series color (compareColors.ts),
  // the same one its header chip and chart lines use -- the swatch here
  // is what ties this table row back to "which line is this" without
  // repeating the chip row's icon+name a second time in a different
  // visual language.
  let { name, colorVar } = $props();

  let c = $derived(live.frame?.containers?.[name]);
  let m = $derived(c?.metrics ?? {});
  let ts = $derived(live.frame?.ts ?? 0);

  function tweenTo(tween, value) {
    tween.set(value, { duration: motion.reduced ? 0 : live.glideMs, easing: linear });
  }

  let cpuTween = new Tween(0, { duration: live.glideMs, easing: linear });
  let coresTween = new Tween(0, { duration: live.glideMs, easing: linear });
  let memBytesTween = new Tween(0, { duration: live.glideMs, easing: linear });
  let memLimitPctTween = new Tween(0, { duration: live.glideMs, easing: linear });
  let netRxTween = new Tween(0, { duration: live.glideMs, easing: linear });
  let netTxTween = new Tween(0, { duration: live.glideMs, easing: linear });
  let ioReadTween = new Tween(0, { duration: live.glideMs, easing: linear });
  let ioWriteTween = new Tween(0, { duration: live.glideMs, easing: linear });

  $effect(() => tweenTo(cpuTween, m['cpu.pct'] ?? 0));
  $effect(() => tweenTo(coresTween, m['cpu.cores'] ?? 0));
  $effect(() => tweenTo(memBytesTween, m['mem.bytes'] ?? 0));
  $effect(() => tweenTo(memLimitPctTween, m['mem.limit_pct'] ?? 0));
  $effect(() => tweenTo(netRxTween, m['net.rx_bps'] ?? 0));
  $effect(() => tweenTo(netTxTween, m['net.tx_bps'] ?? 0));
  $effect(() => tweenTo(ioReadTween, m['io.read_bps'] ?? 0));
  $effect(() => tweenTo(ioWriteTween, m['io.write_bps'] ?? 0));

  // coresLabel reads its own Tween like every sibling value above --
  // it used to be the one figure in this row still stepping per
  // arrival. fmtCores' own <0.05 blank rule applies to the TWEENED
  // reading, so the annotation fades in the moment the glide clears
  // the rounding floor, never on a raw pre-glide target.
  let coresLabel = $derived(fmtCores(coresTween.current));
</script>

<tr class="compare-member-row">
  <td class="compare-member-row__name-cell">
    <span class="compare-member-row__swatch" style="background: var({colorVar})" aria-hidden="true"></span>
    <ContainerIcon {name} icon={c?.icon} size={18} />
    <span class="compare-member-row__name-text">{name}</span>
  </td>
  {#if c}
    <td><HealthDot status={containerHealthStatus(c.state, c.health)} label={c.state} /></td>
    <td class="tabular-nums compare-member-row__num">
      <div class="compare-member-row__stacked-inline">
        <span>{fmtPct(cpuTween.current)}</span>
        {#if coresLabel}<span class="compare-member-row__muted">{coresLabel}</span>{/if}
      </div>
    </td>
    <td class="tabular-nums compare-member-row__num">
      <div class="compare-member-row__stacked-inline">
        <span>{fmtBytes(memBytesTween.current)}</span>
        {#if m['mem.limit_bytes'] !== undefined}
          <span class="compare-member-row__muted">({fmtPct(memLimitPctTween.current)} of limit)</span>
        {/if}
      </div>
    </td>
    <td class="tabular-nums compare-member-row__nowrap compare-member-row__num">
      <div class="compare-member-row__stacked">
        <span>&darr; {fmtRate(netRxTween.current)}</span>
        <span class="compare-member-row__muted">&uarr; {fmtRate(netTxTween.current)}</span>
      </div>
    </td>
    <td class="tabular-nums compare-member-row__nowrap compare-member-row__num">
      <div class="compare-member-row__stacked">
        <span>r {fmtRate(ioReadTween.current)}</span>
        <span class="compare-member-row__muted">w {fmtRate(ioWriteTween.current)}</span>
      </div>
    </td>
    <td class="tabular-nums compare-member-row__num">{m['pids'] !== undefined ? Math.round(m['pids']) : '—'}</td>
    <td class="tabular-nums compare-member-row__nowrap compare-member-row__num">
      {m['meta.started_at'] !== undefined ? fmtDuration(ts - m['meta.started_at']) : '—'}
    </td>
  {:else}
    <td colspan="7" class="microlabel compare-member-row__gone">no longer present</td>
  {/if}
</tr>

<style>
  .compare-member-row td {
    padding: 0.5rem 0.6rem;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 6%, transparent);
    font-size: 0.82rem;
    vertical-align: middle;
    white-space: nowrap;
  }
  .compare-member-row__name-cell {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-weight: 500;
  }
  .compare-member-row__swatch {
    display: inline-block;
    width: 8px;
    height: 8px;
    border-radius: 2px;
    flex-shrink: 0;
  }
  .compare-member-row__name-text {
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .compare-member-row__num {
    text-align: right;
  }
  .compare-member-row__nowrap {
    white-space: nowrap;
  }
  .compare-member-row__stacked {
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    line-height: 1.3;
  }
  .compare-member-row__stacked-inline {
    display: flex;
    align-items: baseline;
    justify-content: flex-end;
    gap: 0.35rem;
  }
  .compare-member-row__muted {
    color: var(--ink-2);
    font-size: 0.75rem;
  }
  .compare-member-row__gone {
    color: var(--ink-2);
    text-align: left;
  }
</style>
