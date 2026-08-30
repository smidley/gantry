<!--
  Maintenance: Gantry's first WRITE surface -- updates (read-only, a
  link out to Unraid's own UI), then image cleanup, then non-running
  container cleanup. Every destructive action (remove/prune, either
  resource) routes through the SAME confirm dialog contract: it states
  exactly what's about to be deleted (count, size when known, the full
  list) before anything happens, and only a real click of its OWN
  confirm button proceeds -- see ConfirmDialog's own doc. That's
  deliberate: this page itself stays a calm, plain workshop table --
  quiet trigger buttons, no warning-tape red anywhere outside the one
  dialog that actually needs it -- because the one moment that matters
  (the confirm click) is where all of that weight belongs, not smeared
  across the whole page.

  Images/containers data is a plain GET snapshot (fetchImages/
  fetchContainersMaintenance), not the live SSE frame -- neither list
  needs second-by-second ticking, and re-fetching after every mutation
  is simpler and more robust than patching summary counts locally (see
  loadImages/loadContainers, called again after every confirmed remove/
  prune). Updates is the one exception: it reads
  live.frame directly (update_status/changelog_url already ride on every
  ContainerDTO), since that data already exists for free on every frame
  and nothing here ever mutates it.

  readOnly is probed once, for both cards -- see api.ts's probeReadOnly
  for why a probe is the only way to know (GANTRY_READ_ONLY has no GET-
  visible hint anywhere) and why it can never risk a real mutation.
-->
<script>
  import { onMount } from 'svelte';
  import { live } from '../lib/sse.svelte';
  import {
    fetchImages,
    removeImages,
    pruneImages,
    fetchContainersMaintenance,
    removeContainersMaintenance,
    pruneContainersMaintenance,
    probeReadOnly,
  } from '../lib/api';
  import {
    removableImages,
    sumImageBytes,
    imagesMatchingPruneMode,
    sortImagesBySize,
    managedBadge,
    hasKeepWarning,
    containerAge,
    containersMatchingPruneMode,
    sortContainersByAge,
    isHttpUrl,
  } from '../lib/maintenance';
  import { fmtBytes, fmtRelTime } from '../lib/format';
  import { describeExitCode } from '../lib/exitCode';
  import ContainerIcon from '../components/ContainerIcon.svelte';
  import ConfirmDialog from '../components/ConfirmDialog.svelte';

  const EMPTY_IMAGES_SUMMARY = { in_use: 0, unused: 0, dangling: 0, reclaimable_bytes: 0, note: '' };
  const EMPTY_CONTAINERS_SUMMARY = { exited: 0, created: 0, dead: 0 };

  let readOnly = $state(false);
  onMount(() => {
    probeReadOnly().then((v) => {
      readOnly = v;
    });
  });

  // --- Updates: a plain read of the live frame, nothing fetched -------
  let updateEntries = $derived(
    Object.entries(live.frame?.containers ?? {})
      .filter(([, c]) => c.update_status === 'available')
      .map(([name, c]) => ({ name, image: c.image, icon: c.icon, changelogUrl: c.changelog_url }))
      .sort((a, b) => a.name.localeCompare(b.name)),
  );

  // --- Images -----------------------------------------------------------
  let images = $state([]);
  let imagesSummary = $state(EMPTY_IMAGES_SUMMARY);
  let imagesLoading = $state(true);
  let imagesFailed = $state(false);
  let selectedImageIds = $state(new Set());
  let imageDialog = $state(null); // { kind: 'remove' | 'prune', mode?, targets }
  let imagePending = $state(false);
  let imageDialogError = $state(null);
  let imageResults = $state(null);

  async function loadImages() {
    imagesLoading = true;
    imagesFailed = false;
    try {
      const dto = await fetchImages();
      images = dto.images;
      imagesSummary = dto.summary;
    } catch {
      imagesFailed = true;
    } finally {
      imagesLoading = false;
    }
  }
  onMount(loadImages);

  let removableImagesList = $derived(sortImagesBySize(removableImages(images)));
  let selectedImagesList = $derived(removableImagesList.filter((im) => selectedImageIds.has(im.full_id)));
  // Sorted the same way removableImagesList is -- a prune button's own
  // confirm dialog previews these lists directly (imageDialogCopy), and
  // it must read in the same order the table beneath it already does.
  let danglingImages = $derived(sortImagesBySize(imagesMatchingPruneMode(images, 'dangling')));
  let unusedImages = $derived(sortImagesBySize(imagesMatchingPruneMode(images, 'unused')));

  function toggleImageSelected(id) {
    const next = new Set(selectedImageIds);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    selectedImageIds = next;
  }

  // (im.repo_tags ?? []): GET /api/images' own list entries always carry
  // a populated repo_tags (server-side digest/"<none>" fallback -- see
  // handleImagesList), but POST /api/images/prune's DeletedImage.repo_tags
  // is the RAW field straight off the fake/real image with no such
  // fallback -- nil for a truly untagged image, which encodes to JSON
  // null, not []. This same helper renders both, so it has to tolerate
  // either shape.
  function imageLabel(im) {
    return (im.repo_tags ?? []).join(', ') || im.id;
  }

  function imageDialogCopy(d) {
    const n = d.targets.length;
    const s = n === 1 ? '' : 's';
    const bytes = fmtBytes(sumImageBytes(d.targets));
    const verb = d.kind === 'remove' ? 'removes' : 'prunes';
    const noun = d.kind === 'remove' ? `${n} image${s}` : `${n} ${d.mode} image${s}`;
    return {
      title: `${d.kind === 'remove' ? 'Remove' : 'Prune'} ${noun}?`,
      description: `This permanently ${verb} ${n} image${s}, freeing up to ${bytes}.`,
      confirmLabel: `${d.kind === 'remove' ? 'Remove' : 'Prune'} ${n} image${s}`,
      items: d.targets.map((im) => ({
        id: im.full_id,
        primary: imageLabel(im),
        secondary: `${im.id} · ${fmtBytes(im.size_bytes)} · ${fmtRelTime(im.created)}`,
      })),
    };
  }

  function openImageRemoveDialog() {
    if (selectedImagesList.length === 0) return;
    imageDialogError = null;
    imageDialog = { kind: 'remove', targets: selectedImagesList };
  }
  function openImagePruneDialog(mode) {
    const targets = mode === 'dangling' ? danglingImages : unusedImages;
    if (targets.length === 0) return;
    imageDialogError = null;
    imageDialog = { kind: 'prune', mode, targets };
  }
  function closeImageDialog() {
    if (imagePending) return;
    imageDialog = null;
  }

  async function confirmImageDialog() {
    if (!imageDialog) return;
    imagePending = true;
    imageDialogError = null;
    try {
      const targets = imageDialog.targets;
      if (imageDialog.kind === 'remove') {
        const results = await removeImages(targets.map((im) => im.full_id));
        imageResults = {
          reclaimedBytes: undefined,
          entries: results.map((r) => ({
            id: r.id,
            label: imageLabel(targets.find((im) => im.full_id === r.id) ?? { repo_tags: [], id: r.id }),
            ok: r.ok,
            error: r.error,
          })),
        };
      } else {
        const result = await pruneImages(imageDialog.mode);
        imageResults = {
          reclaimedBytes: result.reclaimed_bytes,
          entries: [
            ...result.deleted.map((d) => ({ id: d.id, label: imageLabel(d), ok: true })),
            ...result.errors.map((e) => ({ id: '', label: e, ok: false, error: e })),
          ],
        };
      }
      imageDialog = null;
      selectedImageIds = new Set();
      await loadImages();
    } catch (err) {
      imageDialogError = err instanceof Error ? err.message : 'Something went wrong.';
    } finally {
      imagePending = false;
    }
  }

  // --- Containers ---------------------------------------------------------
  let containers = $state([]);
  let containersSummary = $state(EMPTY_CONTAINERS_SUMMARY);
  let containersLoading = $state(true);
  let containersFailed = $state(false);
  let selectedContainerIds = $state(new Set());
  let containerAgeFilterHours = $state('');
  let containerDialog = $state(null);
  let containerPending = $state(false);
  let containerDialogError = $state(null);
  let containerResults = $state(null);

  async function loadContainers() {
    containersLoading = true;
    containersFailed = false;
    try {
      const dto = await fetchContainersMaintenance();
      containers = dto.containers;
      containersSummary = dto.summary;
    } catch {
      containersFailed = true;
    } finally {
      containersLoading = false;
    }
  }
  onMount(loadContainers);

  let sortedContainers = $derived(sortContainersByAge(containers));
  let selectedContainersList = $derived(sortedContainers.filter((ct) => selectedContainerIds.has(ct.full_id)));
  // Sorted the same way sortedContainers is -- see danglingImages' own
  // doc, same reasoning, mirrored for containers.
  let exitedContainers = $derived(sortContainersByAge(containersMatchingPruneMode(containers, 'exited')));
  let createdContainers = $derived(sortContainersByAge(containersMatchingPruneMode(containers, 'created')));

  function toggleContainerSelected(id) {
    const next = new Set(selectedContainerIds);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    selectedContainerIds = next;
  }

  function parsedAgeFilterHours() {
    const n = Number(containerAgeFilterHours);
    return Number.isFinite(n) && n > 0 ? Math.floor(n) : 0;
  }

  function containerDialogCopy(d) {
    const n = d.targets.length;
    const s = n === 1 ? '' : 's';
    const verb = d.kind === 'remove' ? 'removes' : 'prunes';
    const noun = d.kind === 'remove' ? `${n} container${s}` : `${n} ${d.mode} container${s}`;
    return {
      title: `${d.kind === 'remove' ? 'Remove' : 'Prune'} ${noun}?`,
      description: `This permanently ${verb} ${n} container${s}. Any anonymous volumes they own are orphaned, not removed.`,
      caveat:
        d.kind === 'prune' && d.hours > 0
          ? `Only containers older than ${d.hours}h will actually be removed — the exact set is confirmed after running.`
          : '',
      confirmLabel: `${d.kind === 'remove' ? 'Remove' : 'Prune'} ${n} container${s}`,
      items: d.targets.map((ct) => ({
        id: ct.full_id,
        primary: ct.name,
        secondary: `${ct.image} · ${fmtRelTime(containerAge(ct))}`,
        warning: managedBadge(ct.managed) ?? (ct.restart_policy ? 'restarts on boot' : ''),
      })),
    };
  }

  function openContainerRemoveDialog() {
    if (selectedContainersList.length === 0) return;
    containerDialogError = null;
    containerDialog = { kind: 'remove', targets: selectedContainersList };
  }
  function openContainerPruneDialog(mode) {
    const targets = mode === 'exited' ? exitedContainers : createdContainers;
    if (targets.length === 0) return;
    containerDialogError = null;
    containerDialog = { kind: 'prune', mode, hours: parsedAgeFilterHours(), targets };
  }
  function closeContainerDialog() {
    if (containerPending) return;
    containerDialog = null;
  }

  // parseContainerPruneError splits a prune result's own "<full_id>:
  // <message>" error string (see docker.PruneContainers/fake's own
  // Errors shape) back into an id (to resolve a friendly name against)
  // and the message alone -- full container ids are 64 lowercase-hex
  // characters with no colon of their own, so the FIRST ": " is always
  // exactly the id/message boundary, never a false split inside the
  // message itself.
  function parseContainerPruneError(raw) {
    const idx = raw.indexOf(': ');
    if (idx === -1) return { id: '', message: raw };
    return { id: raw.slice(0, idx), message: raw.slice(idx + 2) };
  }

  async function confirmContainerDialog() {
    if (!containerDialog) return;
    containerPending = true;
    containerDialogError = null;
    try {
      const targets = containerDialog.targets;
      if (containerDialog.kind === 'remove') {
        const results = await removeContainersMaintenance(targets.map((ct) => ct.full_id));
        containerResults = {
          entries: results.map((r) => ({
            id: r.id,
            label: targets.find((ct) => ct.full_id === r.id)?.name ?? r.id,
            ok: r.ok,
            error: r.error,
          })),
        };
      } else {
        const result = await pruneContainersMaintenance(containerDialog.mode, containerDialog.hours);
        containerResults = {
          entries: [
            ...result.deleted.map((d) => ({ id: d.id, label: d.name, ok: true })),
            ...result.errors.map((raw) => {
              const { id, message } = parseContainerPruneError(raw);
              const known = containers.find((ct) => ct.full_id === id);
              return { id, label: known?.name ?? id ?? raw, ok: false, error: message };
            }),
          ],
        };
      }
      containerDialog = null;
      selectedContainerIds = new Set();
      await loadContainers();
    } catch (err) {
      containerDialogError = err instanceof Error ? err.message : 'Something went wrong.';
    } finally {
      containerPending = false;
    }
  }

  function exitCodeText(ct) {
    if (ct.exit_code === undefined) return '—';
    const desc = describeExitCode(ct.exit_code);
    return desc ? `${ct.exit_code} (${desc})` : String(ct.exit_code);
  }
</script>

<div class="maintenance-view">
  <h1 class="page-title">Maintenance</h1>

  {#if readOnly}
    <p class="microlabel maintenance-view__readonly">
      Gantry is running in read-only mode (GANTRY_READ_ONLY) — removal and pruning are disabled below.
    </p>
  {/if}

  <!-- Updates: read-only by design -- Gantry only flags what's available,
       actually updating stays Unraid's own job. -->
  <div class="card maintenance-card">
    <div class="maintenance-card__head">
      <span class="microlabel">Updates &middot; {updateEntries.length}</span>
    </div>
    <p class="microlabel maintenance-card__caption">
      Updating happens in Unraid's own Docker UI — Gantry only flags what's available here.
    </p>
    {#if updateEntries.length === 0}
      <p class="microlabel maintenance-card__empty">Every container is on its latest known image.</p>
    {:else}
      <ul class="maintenance-updates">
        {#each updateEntries as entry (entry.name)}
          <li class="maintenance-updates__row">
            <span class="maintenance-updates__name">
              <ContainerIcon name={entry.name} icon={entry.icon} size={18} />
              {entry.name}
            </span>
            <span class="maintenance-updates__image">{entry.image}</span>
            {#if isHttpUrl(entry.changelogUrl)}
              <a class="maintenance-updates__changelog" href={entry.changelogUrl} target="_blank" rel="noopener noreferrer">
                Changelog &rarr;
              </a>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
  </div>

  <!-- Images -->
  <div class="card maintenance-card">
    <div class="maintenance-card__head">
      <span class="microlabel">
        Images &middot; in-use {imagesSummary.in_use} &middot; unused {imagesSummary.unused} &middot; dangling {imagesSummary.dangling}
      </span>
      {#if imagesSummary.reclaimable_bytes > 0}
        <span class="microlabel maintenance-card__reclaim" title={imagesSummary.note}
          >up to {fmtBytes(imagesSummary.reclaimable_bytes)} reclaimable</span
        >
      {/if}
    </div>

    {#if imageResults}
      {@render resultsPanel(imageResults, () => (imageResults = null))}
    {/if}

    {#if imagesFailed}
      <p class="microlabel maintenance-card__error">Couldn't load images. Try again shortly.</p>
    {:else if imagesLoading}
      <p class="microlabel maintenance-card__empty">Loading…</p>
    {:else if removableImagesList.length === 0}
      <p class="microlabel maintenance-card__empty">No unused or dangling images.</p>
    {:else}
      <div class="maintenance-actions">
        <button type="button" disabled={readOnly || selectedImagesList.length === 0} onclick={openImageRemoveDialog}>
          Remove selected ({selectedImagesList.length})
        </button>
        <button type="button" disabled={readOnly || danglingImages.length === 0} onclick={() => openImagePruneDialog('dangling')}>
          Prune dangling ({danglingImages.length})
        </button>
        <button type="button" disabled={readOnly || unusedImages.length === 0} onclick={() => openImagePruneDialog('unused')}>
          Prune unused ({unusedImages.length})
        </button>
      </div>

      <!-- Desktop (>=768px): a plain fixed-column table -- see
           .maintenance-table's own doc. Mobile: stacked rail rows below,
           same convention Containers.svelte's own fleet table uses for
           the same reason -- a 6-column table has no good narrow-
           viewport rendering, scrolling it sideways included. -->
      <div class="maintenance-table-wrap hidden md:block">
        <table class="maintenance-table maintenance-table--images">
          <colgroup>
            <col style="width: 2.5rem" />
            <col style="width: 6.5rem" />
            <col />
            <col style="width: 5.5rem" />
            <col style="width: 5rem" />
            <col style="width: 5.5rem" />
          </colgroup>
          <thead>
            <tr>
              <th class="microlabel"><span class="sr-only">Select</span></th>
              <th class="microlabel">ID</th>
              <th class="microlabel">Tags</th>
              <th class="microlabel">State</th>
              <th class="microlabel maintenance-table__th--numeric">Size</th>
              <th class="microlabel">Age</th>
            </tr>
          </thead>
          <tbody>
            {#each removableImagesList as im (im.full_id)}
              <tr>
                <td>
                  <input
                    type="checkbox"
                    checked={selectedImageIds.has(im.full_id)}
                    disabled={readOnly}
                    onchange={() => toggleImageSelected(im.full_id)}
                    aria-label={`Select ${imageLabel(im)}`}
                  />
                </td>
                <td class="tabular-nums maintenance-table__mono">{im.id}</td>
                <td class="maintenance-table__tags">{imageLabel(im)}</td>
                <td><span class="maintenance-badge">{im.state}</span></td>
                <td class="tabular-nums maintenance-table__th--numeric">{fmtBytes(im.size_bytes)}</td>
                <td class="tabular-nums">{fmtRelTime(im.created)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>

      <div class="maintenance-mobile-list flex md:hidden">
        {#each removableImagesList as im (im.full_id)}
          <div class="maintenance-mobile-row">
            <input
              type="checkbox"
              checked={selectedImageIds.has(im.full_id)}
              disabled={readOnly}
              onchange={() => toggleImageSelected(im.full_id)}
              aria-label={`Select ${imageLabel(im)}`}
            />
            <div class="maintenance-mobile-row__body">
              <div class="maintenance-mobile-row__title">{imageLabel(im)}</div>
              <div class="maintenance-mobile-row__meta tabular-nums">
                <span class="maintenance-table__mono">{im.id}</span>
                <span class="maintenance-badge">{im.state}</span>
                <span>{fmtBytes(im.size_bytes)}</span>
                <span>{fmtRelTime(im.created)}</span>
              </div>
            </div>
          </div>
        {/each}
      </div>
    {/if}
  </div>

  <!-- Containers -->
  <div class="card maintenance-card">
    <div class="maintenance-card__head">
      <span class="microlabel">
        Containers &middot; exited {containersSummary.exited} &middot; created {containersSummary.created} &middot; dead {containersSummary.dead}
      </span>
    </div>

    {#if containerResults}
      {@render resultsPanel(containerResults, () => (containerResults = null))}
    {/if}

    {#if containersFailed}
      <p class="microlabel maintenance-card__error">Couldn't load containers. Try again shortly.</p>
    {:else if containersLoading}
      <p class="microlabel maintenance-card__empty">Loading…</p>
    {:else if sortedContainers.length === 0}
      <p class="microlabel maintenance-card__empty">No exited, created, or dead containers.</p>
    {:else}
      <div class="maintenance-actions">
        <button type="button" disabled={readOnly || selectedContainersList.length === 0} onclick={openContainerRemoveDialog}>
          Remove selected ({selectedContainersList.length})
        </button>
        <label class="maintenance-age-filter">
          <span class="microlabel">Older than</span>
          <input type="number" min="0" step="1" placeholder="0" bind:value={containerAgeFilterHours} disabled={readOnly} />
          <span class="microlabel">hours</span>
        </label>
        <button type="button" disabled={readOnly || exitedContainers.length === 0} onclick={() => openContainerPruneDialog('exited')}>
          Prune exited ({exitedContainers.length})
        </button>
        <button type="button" disabled={readOnly || createdContainers.length === 0} onclick={() => openContainerPruneDialog('created')}>
          Prune created ({createdContainers.length})
        </button>
      </div>

      <div class="maintenance-table-wrap hidden md:block">
        <table class="maintenance-table maintenance-table--containers">
          <colgroup>
            <col style="width: 2.5rem" />
            <col style="width: 4.5rem" />
            <col style="width: 15rem" />
            <col />
            <col style="width: 6rem" />
            <col style="width: 10rem" />
          </colgroup>
          <thead>
            <tr>
              <th class="microlabel"><span class="sr-only">Select</span></th>
              <th class="microlabel">State</th>
              <th class="microlabel">Name</th>
              <th class="microlabel">Image</th>
              <th class="microlabel">Age</th>
              <th class="microlabel">Exit code</th>
            </tr>
          </thead>
          <tbody>
            {#each sortedContainers as ct (ct.full_id)}
              {@const icon = live.frame?.containers?.[ct.name]?.icon}
              {@const managed = managedBadge(ct.managed)}
              <tr class:maintenance-row--warn={hasKeepWarning(ct)}>
                <td>
                  <input
                    type="checkbox"
                    checked={selectedContainerIds.has(ct.full_id)}
                    disabled={readOnly}
                    onchange={() => toggleContainerSelected(ct.full_id)}
                    aria-label={`Select ${ct.name}`}
                  />
                </td>
                <td>{ct.state}</td>
                <td class="maintenance-table__name">
                  <ContainerIcon name={ct.name} {icon} size={16} />
                  <span>{ct.name}</span>
                  {#if managed}<span class="maintenance-badge">{managed}</span>{/if}
                  {#if ct.restart_policy}
                    <span class="maintenance-badge maintenance-badge--warn" title={ct.restart_policy}>restarts on boot</span>
                  {/if}
                </td>
                <td class="maintenance-table__tags">{ct.image}</td>
                <td class="tabular-nums">{fmtRelTime(containerAge(ct))}</td>
                <td class="tabular-nums">{exitCodeText(ct)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>

      <div class="maintenance-mobile-list flex md:hidden">
        {#each sortedContainers as ct (ct.full_id)}
          {@const icon = live.frame?.containers?.[ct.name]?.icon}
          {@const managed = managedBadge(ct.managed)}
          <div class="maintenance-mobile-row" class:maintenance-row--warn={hasKeepWarning(ct)}>
            <input
              type="checkbox"
              checked={selectedContainerIds.has(ct.full_id)}
              disabled={readOnly}
              onchange={() => toggleContainerSelected(ct.full_id)}
              aria-label={`Select ${ct.name}`}
            />
            <div class="maintenance-mobile-row__body">
              <div class="maintenance-table__name">
                <ContainerIcon name={ct.name} {icon} size={16} />
                <span class="maintenance-mobile-row__title">{ct.name}</span>
                {#if managed}<span class="maintenance-badge">{managed}</span>{/if}
                {#if ct.restart_policy}
                  <span class="maintenance-badge maintenance-badge--warn" title={ct.restart_policy}>restarts on boot</span>
                {/if}
              </div>
              <div class="maintenance-mobile-row__meta tabular-nums">
                <span>{ct.state}</span>
                <span>{ct.image}</span>
                <span>{fmtRelTime(containerAge(ct))}</span>
                <span>{exitCodeText(ct)}</span>
              </div>
            </div>
          </div>
        {/each}
      </div>
    {/if}
  </div>
</div>

{#snippet resultsPanel(results, onDismiss)}
  <div class="maintenance-results">
    <div class="maintenance-results__head">
      <span class="microlabel">Result</span>
      <button type="button" class="maintenance-results__dismiss" onclick={onDismiss} aria-label="Dismiss result">&times;</button>
    </div>
    {#if results.reclaimedBytes !== undefined}
      <p class="maintenance-results__note">Reclaimed up to {fmtBytes(results.reclaimedBytes)}.</p>
    {/if}
    <ul class="maintenance-results__list">
      {#each results.entries as entry, i (entry.id || `${i}-${entry.label}`)}
        <li class:maintenance-results__row--error={!entry.ok}>
          <span>{entry.ok ? '✓' : '✕'} {entry.label}</span>
          {#if entry.error}<span class="maintenance-results__detail">{entry.error}</span>{/if}
        </li>
      {/each}
    </ul>
    {#if results.entries.some((e) => e.ok)}
      <a class="maintenance-results__events-link" href="#/events">logged to Events &rarr;</a>
    {/if}
  </div>
{/snippet}

{#if imageDialog}
  {@const copy = imageDialogCopy(imageDialog)}
  <ConfirmDialog
    title={copy.title}
    description={copy.description}
    confirmLabel={copy.confirmLabel}
    items={copy.items}
    pending={imagePending}
    error={imageDialogError}
    onConfirm={confirmImageDialog}
    onCancel={closeImageDialog}
  />
{/if}

{#if containerDialog}
  {@const copy = containerDialogCopy(containerDialog)}
  <ConfirmDialog
    title={copy.title}
    description={copy.description}
    caveat={copy.caveat}
    confirmLabel={copy.confirmLabel}
    items={copy.items}
    pending={containerPending}
    error={containerDialogError}
    onConfirm={confirmContainerDialog}
    onCancel={closeContainerDialog}
  />
{/if}

<style>
  .maintenance-view {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }
  .maintenance-view__readonly {
    margin: 0;
    padding: 0.6rem 0.85rem;
    border-radius: 6px;
    border: 1px solid color-mix(in oklab, var(--status-warning) 40%, transparent);
    background: color-mix(in oklab, var(--status-warning) 10%, transparent);
    color: var(--ink);
    text-transform: none;
    letter-spacing: normal;
  }
  .maintenance-card {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
  }
  .maintenance-card__head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 0.4rem;
  }
  .maintenance-card__reclaim {
    color: var(--ink-2);
  }
  .maintenance-card__caption {
    margin: 0;
    text-transform: none;
    letter-spacing: normal;
  }
  .maintenance-card__empty,
  .maintenance-card__error {
    margin: 0;
  }
  .maintenance-card__error {
    color: var(--status-warning);
  }
  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
  }

  /* Updates: a plain rail list, same hairline-between-rows convention as
     the disk list/event feed -- this card has no destructive action in
     it at all, so it stays the quietest one on the page. */
  .maintenance-updates {
    margin: 0;
    padding: 0;
    list-style: none;
    display: flex;
    flex-direction: column;
  }
  .maintenance-updates__row {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    flex-wrap: wrap;
    padding: 0.5rem 0;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 8%, transparent);
  }
  .maintenance-updates__row:last-child {
    border-bottom: none;
    padding-bottom: 0;
  }
  .maintenance-updates__row:first-child {
    padding-top: 0;
  }
  .maintenance-updates__name {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    font-weight: 500;
    min-width: 8rem;
  }
  .maintenance-updates__image {
    font-family: var(--font-mono);
    font-size: 0.78rem;
    color: var(--ink-2);
    flex: 1;
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .maintenance-updates__changelog {
    font-size: 0.82rem;
    color: var(--series-1);
    text-decoration: none;
    white-space: nowrap;
  }
  .maintenance-updates__changelog:hover {
    text-decoration: underline;
  }

  /* Trigger buttons: deliberately quiet -- plain outline, same as
     events-view__load-more -- the confirm dialog's own button is the
     only loud control on this page (see its own doc). */
  .maintenance-actions {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 0.6rem;
  }
  .maintenance-actions button {
    min-height: 40px;
    padding: 0 1rem;
    border-radius: 6px;
    border: 1px solid color-mix(in oklab, var(--ink) 18%, transparent);
    background: transparent;
    color: var(--ink);
    font-size: 0.82rem;
    cursor: pointer;
    white-space: nowrap;
  }
  .maintenance-actions button:hover:not(:disabled) {
    background: color-mix(in oklab, var(--ink) 6%, transparent);
  }
  .maintenance-actions button:disabled {
    opacity: 0.45;
    cursor: default;
  }
  .maintenance-age-filter {
    display: flex;
    align-items: center;
    gap: 0.35rem;
  }
  .maintenance-age-filter input {
    width: 4rem;
    min-height: 40px;
    padding: 0 0.5rem;
    border-radius: 6px;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: var(--surface);
    color: var(--ink);
    font-size: 0.85rem;
  }

  /* Desktop only (hidden md:block, template) -- a 6-column table has no
     good narrow-viewport rendering, scrolling it sideways included, so
     mobile gets the stacked rail rows below instead (Containers.svelte's
     own fleet table split, same reason). overflow-x:auto is still real
     insurance at an in-between width (>=768px but still cramped), not
     the primary mobile story. */
  .maintenance-table-wrap {
    overflow-x: auto;
  }
  /* table-layout:fixed + the colgroups above pin every column's own
     width regardless of content -- a long tag/image string's own
     overflow-wrap:anywhere (below) would otherwise shrink that whole
     column instead of wrapping WITHIN a fixed one, pushing State/Size/
     Age around unpredictably. Tags/Image is the one column left
     un-widthed in each colgroup, matching Containers.svelte's own
     "image gets the remaining slack" convention. */
  .maintenance-table {
    width: 100%;
    border-collapse: collapse;
    table-layout: fixed;
  }
  .maintenance-table--images {
    min-width: 34rem;
  }
  .maintenance-table--containers {
    min-width: 45rem;
  }
  .maintenance-table th {
    text-align: left;
    padding: 0.5rem 0.6rem;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 12%, transparent);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .maintenance-table td {
    padding: 0.45rem 0.6rem;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 6%, transparent);
    font-size: 0.85rem;
    vertical-align: middle;
  }
  .maintenance-table tbody tr:last-child td {
    border-bottom: none;
  }
  .maintenance-table__th--numeric {
    text-align: right;
  }
  .maintenance-table__mono {
    font-family: var(--font-mono);
    font-size: 0.78rem;
  }
  .maintenance-table__tags {
    overflow-wrap: anywhere;
  }
  .maintenance-table__name {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    flex-wrap: wrap;
  }
  /* A managed/restart-policy row gets a quiet amber rail rather than a
     loud full-row fill -- distinct enough to notice while scanning,
     nowhere near the confirm dialog's own weight. */
  .maintenance-row--warn {
    box-shadow: inset 3px 0 0 0 var(--status-warning);
  }
  .maintenance-badge {
    font-family: var(--font-mono);
    font-size: 0.68rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    padding: 0.15rem 0.45rem;
    border-radius: 999px;
    background: color-mix(in oklab, var(--ink) 8%, transparent);
    color: var(--ink-2);
    white-space: nowrap;
  }
  .maintenance-badge--warn {
    background: color-mix(in oklab, var(--status-warning) 18%, transparent);
    color: var(--status-warning);
  }

  /* Mobile (<768px): stacked rail rows, same hairline-between-rows
     convention as Containers.svelte's own mobile card list -- the
     checkbox sits OUTSIDE the row's own text block (a sibling, not
     nested inside it), matching that same precedent exactly, so it
     never fights with anything else in the row for tap targets. */
  .maintenance-mobile-list {
    flex-direction: column;
  }
  .maintenance-mobile-row {
    padding: 0.65rem 0;
    display: flex;
    align-items: flex-start;
    gap: 0.6rem;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 8%, transparent);
  }
  .maintenance-mobile-row:last-child {
    border-bottom: none;
    padding-bottom: 0;
  }
  .maintenance-mobile-row:first-child {
    padding-top: 0;
  }
  .maintenance-mobile-row input[type='checkbox'] {
    margin-top: 0.2rem;
  }
  .maintenance-mobile-row__body {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
  }
  .maintenance-mobile-row__title {
    font-weight: 500;
    overflow-wrap: anywhere;
  }
  .maintenance-mobile-row__meta {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    font-size: 0.78rem;
    color: var(--ink-2);
    overflow-wrap: anywhere;
  }

  /* Results: a plain rail panel, same visual language as the rest of
     the card -- ok rows read as done and unremarkable, error rows are
     the one thing worth a reader's second look here. */
  .maintenance-results {
    padding: 0.65rem 0.75rem;
    border-radius: 6px;
    border: 1px solid color-mix(in oklab, var(--ink) 12%, transparent);
    background: color-mix(in oklab, var(--ink) 3%, transparent);
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
  }
  .maintenance-results__head {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  .maintenance-results__dismiss {
    border: none;
    background: transparent;
    color: var(--ink-2);
    cursor: pointer;
    font-size: 0.9rem;
    line-height: 1;
    padding: 0.2rem;
  }
  .maintenance-results__dismiss:hover {
    color: var(--ink);
  }
  .maintenance-results__note {
    margin: 0;
    font-size: 0.85rem;
    color: var(--ink-2);
  }
  .maintenance-results__list {
    margin: 0;
    padding: 0;
    list-style: none;
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
  }
  .maintenance-results__list li {
    display: flex;
    align-items: baseline;
    gap: 0.5rem;
    flex-wrap: wrap;
    font-size: 0.82rem;
  }
  .maintenance-results__row--error {
    color: var(--status-critical);
  }
  .maintenance-results__detail {
    color: var(--ink-2);
    font-size: 0.78rem;
  }
  .maintenance-results__events-link {
    align-self: flex-start;
    font-size: 0.78rem;
    color: var(--series-1);
    text-decoration: none;
  }
  .maintenance-results__events-link:hover {
    text-decoration: underline;
  }
</style>
