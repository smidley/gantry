<!--
  LiveValue: one live numeric TEXT value, rendered through its own
  cadence-driven Tween (live.glideMs/linear -- see streamdriver.ts's
  "Cadence-driven glide" doc) so the number sweeps between ~2s frame
  arrivals instead of stepping once per arrival. This is the house
  pattern for a bare text value's glide, extracted from the exact
  Tween-in-a-component shape TopBarRow/ContainerRow/CompareMemberRow
  already use for their row values -- it exists for the values that
  DON'T live in a per-entity row component: a view header's host
  total, a per-disk usage figure.

  The instance must be KEYED on the identity of the value it renders,
  the same reason TopBarList keys its rows on entity+metric: a
  surviving instance glides VALUE changes, so a caller that can swap
  which metric the number means (the Metrics page's resource tabs)
  must wrap the instance in {#key} on that identity -- otherwise a
  cpu->mem switch glides 14 -> 6.2e9 through a nonsense sweep of
  intermediate readings formatted in the new unit.

  live=false (a fetched/historical window) and reduced motion both
  collapse the duration to 0, which makes .set() apply synchronously
  (svelte/motion's own duration===0 rule) -- the same static-when-not-
  live contract every other Tween in the app follows.
-->
<script>
  import { untrack } from 'svelte';
  import { Tween } from 'svelte/motion';
  import { linear } from 'svelte/easing';
  import { motion } from '../lib/motion.svelte';
  import { live as liveStore } from '../lib/sse.svelte';

  // value: the live target. undefined/null read as 0 -- same "nothing
  // yet" seed every row component here uses. format: the lib/format.ts
  // formatter tween.current renders through. live: false pins the
  // value static (fetched windows), TopBarRow's own prop contract.
  let { value, format, live = true } = $props();

  // untrack: a deliberate ONE-TIME read to seed the Tween (TopBarRow's
  // own documented constructor contract) -- every later value flows
  // through the $effect below, which reads the prop fresh per run.
  let tween = new Tween(untrack(() => value ?? 0), { duration: liveStore.glideMs, easing: linear });

  $effect(() => {
    const target = value ?? 0;
    const reduced = motion.reduced;
    tween.set(target, { duration: live && !reduced ? liveStore.glideMs : 0, easing: linear });
  });
</script>{format(tween.current)}
