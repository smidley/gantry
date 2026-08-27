<!--
  ContainerIcon: a container's net.unraid.docker.icon image (Community
  Applications sets this label on every template it installs), or a
  square first-letter avatar when there's no icon URL at all, or the
  <img> fails to load -- a stale LAN-only URL, a container not installed
  via CA, or fake-data mode (fake.go's synthetic fleet never sets Icon,
  so every fake container exercises this fallback everywhere this
  component is used, in dev and in the Playwright suite).

  Decorative in every placement this ships in (table rows, cards, the
  detail header, top-consumers rows): the container's own name always
  renders right next to it, so both branches are hidden from assistive
  tech rather than duplicating that name as alt text.
-->
<script>
  import { fallbackLetter } from '../lib/containerIcon';

  let { name, icon, size = 20 } = $props();

  // failed flips true on a load error (404, unreachable LAN host,
  // whatever) so this instance falls back to the letter avatar instead
  // of the browser's own broken-image glyph. Reset whenever `icon`
  // itself changes -- a fresh URL deserves a fresh attempt rather than
  // inheriting an unrelated earlier failure. (Bare `icon;` below is the
  // same read-to-register-a-dependency idiom Containers.svelte's own
  // sort effect uses.)
  let failed = $state(false);
  $effect(() => {
    icon;
    failed = false;
  });
</script>

{#if icon && !failed}
  <img
    class="container-icon"
    src={icon}
    alt=""
    loading="lazy"
    width={size}
    height={size}
    style="width: {size}px; height: {size}px;"
    onerror={() => (failed = true)}
  />
{:else}
  <span
    class="container-icon container-icon--fallback"
    style="width: {size}px; height: {size}px; font-size: {size * 0.55}px;"
    aria-hidden="true"
  >
    {fallbackLetter(name)}
  </span>
{/if}

<style>
  .container-icon {
    flex-shrink: 0;
    border-radius: 4px;
  }
  img.container-icon {
    display: block;
    object-fit: contain;
  }
  .container-icon--fallback {
    display: flex;
    align-items: center;
    justify-content: center;
    background: color-mix(in oklab, var(--ink) 8%, transparent);
    color: var(--ink-2);
    font-family: var(--font-mono);
    font-weight: 600;
    line-height: 1;
  }
</style>
