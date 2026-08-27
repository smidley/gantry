<!--
  Settings: Sources status, the retention editor (Task 10's four fields),
  Gantry's own footprint receipt, the theme control, and an About card.
  Retention is the one piece with real server round-trips (GET on
  mount, PUT on save) -- everything else reads straight off the live
  frame or the theme store.
-->
<script>
  import { onMount } from 'svelte';
  import { live } from '../lib/sse.svelte';
  import { theme } from '../lib/theme.svelte';
  import { liveRing } from '../lib/livering.svelte';
  import { fetchSettings, fetchVersion, putSettings } from '../lib/api';
  import { fmtBytes, fmtPct } from '../lib/format';
  import HealthDot from '../components/HealthDot.svelte';
  import StatTile from '../components/StatTile.svelte';

  const RETENTION_FIELDS = [
    { key: 'r1_hours', label: 'R1 (1 min resolution) retention, hours', min: 1, max: 168 },
    { key: 'r2_days', label: 'R2 (10 min resolution) retention, days', min: 1, max: 90 },
    { key: 'r3_days', label: 'R3 (1 hour resolution) retention, days', min: 30, max: 1095 },
    { key: 'size_cap_mb', label: 'Database size cap, MB', min: 64, max: 4096 },
  ];
  const THEME_OPTIONS = [
    { key: 'system', label: 'System' },
    { key: 'light', label: 'Light' },
    { key: 'dark', label: 'Dark' },
  ];

  let sources = $derived(live.frame?.sources ?? {});
  let sourceNames = $derived(Object.keys(sources).sort());
  let unraidVersion = $derived(live.frame?.unraid_version ?? '');

  let cpuRing = liveRing((f) => f.host?.['gantry.cpu_pct']);
  let rssRing = liveRing((f) => f.host?.['gantry.rss_bytes']);
  let cpuPct = $derived(live.frame?.host?.['gantry.cpu_pct']);
  let rssBytes = $derived(live.frame?.host?.['gantry.rss_bytes']);

  // --- Retention editor -----------------------------------------------
  let retentionLoaded = $state(false);
  let loadError = $state(null);
  let envOverridden = $state(new Set());
  let formValues = $state({});
  let fieldErrors = $state({});
  let saving = $state(false);
  let saveSuccess = $state(false);
  let saveError = $state(null);

  function applySettingsResponse(resp) {
    envOverridden = new Set(resp.env_overridden);
    formValues = Object.fromEntries(RETENTION_FIELDS.map((f) => [f.key, resp.retention[f.key]]));
  }

  async function loadSettings() {
    try {
      const resp = await fetchSettings();
      applySettingsResponse(resp);
      loadError = null;
    } catch {
      loadError = "Couldn't load settings. Try reloading the page.";
    } finally {
      retentionLoaded = true;
    }
  }

  // validateLocal mirrors the server's own per-field range check
  // (retentionFields in api_settings.go) so an out-of-range value gets
  // immediate feedback without a round trip -- the server still has the
  // final word (see saveRetention's catch below for its own errors).
  function validateLocal() {
    const errs = {};
    for (const f of RETENTION_FIELDS) {
      if (envOverridden.has(f.key)) continue;
      const v = formValues[f.key];
      if (!Number.isFinite(v)) errs[f.key] = 'Enter a number.';
      else if (v < f.min || v > f.max) errs[f.key] = `Must be between ${f.min} and ${f.max}.`;
    }
    return errs;
  }

  async function saveRetention(e) {
    e.preventDefault();
    saveSuccess = false;
    saveError = null;

    const localErrors = validateLocal();
    if (Object.keys(localErrors).length > 0) {
      fieldErrors = localErrors;
      return;
    }
    fieldErrors = {};

    saving = true;
    const payload = {};
    for (const f of RETENTION_FIELDS) payload[f.key] = formValues[f.key];
    try {
      const resp = await putSettings(payload);
      applySettingsResponse(resp);
      saveSuccess = true;
    } catch (err) {
      // fields (400, out of range) and envOverridden (409, conflicts
      // with the live env value) are both per-field detail putSettings
      // preserves on the thrown error (see api.ts's own doc) -- render
      // each inline at its own field rather than one generic banner
      // when either is present; a plain 404/500 has neither, so it
      // falls through to the generic saveError banner instead.
      const perField = {};
      if (err?.fields) Object.assign(perField, err.fields);
      if (err?.envOverridden) {
        for (const name of err.envOverridden) {
          perField[name] = 'Locked by an environment variable — reload to see its current value.';
        }
      }
      if (Object.keys(perField).length > 0) {
        fieldErrors = perField;
      } else {
        saveError = err instanceof Error ? err.message : String(err);
      }
    } finally {
      saving = false;
    }
  }

  // --- About -----------------------------------------------------------
  let version = $state(null);
  onMount(() => {
    loadSettings();
    fetchVersion()
      .then((v) => {
        version = v.version;
      })
      .catch(() => {
        version = null;
      });
  });
</script>

<div class="settings-view">
  <h1 class="page-title">Settings</h1>

  <div class="card settings-sources">
    <span class="microlabel">Sources</span>
    <ul class="settings-sources__list">
      {#each sourceNames as name (name)}
        {@const detail = sources[name]}
        {@const ok = detail === 'ok'}
        <li class="settings-sources__row">
          <HealthDot status={ok ? 'good' : 'warning'} label={name} />
          {#if !ok}<span class="settings-sources__detail">{detail}</span>{/if}
        </li>
      {/each}
    </ul>
  </div>

  <!-- novalidate: min/max stay as real DOM attributes (a11y + the
       brief's own "ranges as min/max attrs" contract), but native
       constraint validation would otherwise silently block the submit
       event for an out-of-range value before saveRetention ever runs --
       verified live while building this (typing 999 into R1's 1-168
       range produced no request, no state change, nothing) -- leaving
       this app's OWN styled inline error (validateLocal, below) as the
       only feedback the user ever sees, consistent for every failure
       mode (empty input, out of range, a 400/409 from the server) rather
       than a browser-native tooltip for just one of them. -->
  <form class="card settings-retention" onsubmit={saveRetention} novalidate>
    <span class="microlabel">Retention</span>
    {#if loadError}
      <p class="microlabel settings-retention__error">{loadError}</p>
    {:else if !retentionLoaded}
      <p class="microlabel">Loading…</p>
    {:else}
      <div class="settings-retention__fields">
        {#each RETENTION_FIELDS as f (f.key)}
          {@const locked = envOverridden.has(f.key)}
          <div class="settings-retention__field">
            <label for={`settings-retention-${f.key}`} class="settings-retention__label">
              <span>{f.label}</span>
              {#if locked}<span class="settings-retention__lock">Env locked</span>{/if}
            </label>
            <input
              id={`settings-retention-${f.key}`}
              type="number"
              min={f.min}
              max={f.max}
              bind:value={formValues[f.key]}
              disabled={locked}
            />
            {#if fieldErrors[f.key]}
              <span class="microlabel settings-retention__field-error">{fieldErrors[f.key]}</span>
            {/if}
          </div>
        {/each}
      </div>
      <div class="settings-retention__actions">
        <button type="submit" class="settings-retention__save" disabled={saving}>{saving ? 'Saving…' : 'Save'}</button>
        {#if saveSuccess}<span class="microlabel settings-retention__success">Saved.</span>{/if}
        {#if saveError}<span class="microlabel settings-retention__save-error">{saveError}</span>{/if}
      </div>
    {/if}
  </form>

  <div class="settings-view__row">
    <div class="settings-footprint">
      <span class="microlabel">Gantry footprint</span>
      <div class="settings-footprint__tiles">
        <StatTile label="CPU" value={fmtPct(cpuPct ?? 0)} sparklinePoints={cpuRing.points} />
        <StatTile label="Memory" value={fmtBytes(rssBytes ?? 0)} sparklinePoints={rssRing.points} />
      </div>
      <p class="microlabel settings-footprint__caption">Budget: core &le;2% &middot; RSS &le;100MB</p>
    </div>

    <div class="card settings-theme">
      <span class="microlabel">Theme</span>
      <div class="settings-theme__segmented" role="group" aria-label="Theme">
        {#each THEME_OPTIONS as opt (opt.key)}
          <button
            type="button"
            class="settings-theme__segment"
            class:settings-theme__segment--active={theme.preference === opt.key}
            onclick={() => theme.set(opt.key)}
          >
            {opt.label}
          </button>
        {/each}
      </div>
    </div>

    <div class="card settings-about">
      <span class="microlabel">About</span>
      <dl class="settings-about__list">
        <dt>Version</dt>
        <dd>{version ?? '—'}</dd>
        <dt>Unraid</dt>
        <dd>{unraidVersion || '—'}</dd>
      </dl>
      <a class="settings-about__link" href="https://github.com/smidley/gantry" target="_blank" rel="noopener noreferrer">
        github.com/smidley/gantry
      </a>
    </div>
  </div>
</div>

<style>
  .settings-view {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }
  .settings-sources,
  .settings-retention {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
  }
  .settings-sources__list {
    margin: 0;
    padding: 0;
    list-style: none;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .settings-sources__row {
    display: flex;
    align-items: baseline;
    gap: 0.6rem;
    flex-wrap: wrap;
  }
  .settings-sources__detail {
    font-size: 0.82rem;
    color: var(--ink-2);
  }
  .settings-retention__error {
    color: var(--status-warning);
    margin: 0;
  }
  .settings-retention__fields {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: 0.75rem 1.5rem;
  }
  @media (max-width: 47.9375rem) {
    .settings-retention__fields {
      grid-template-columns: 1fr;
    }
  }
  .settings-retention__field {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
  }
  .settings-retention__label {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.85rem;
  }
  .settings-retention__lock {
    font-family: var(--font-mono);
    font-size: 0.68rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    padding: 0.15rem 0.45rem;
    border-radius: 999px;
    background: color-mix(in oklab, var(--ink) 7%, transparent);
    color: var(--ink-2);
  }
  .settings-retention__field input {
    min-height: 40px;
    padding: 0 0.75rem;
    border-radius: 6px;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: var(--surface);
    color: var(--ink);
    font-size: 0.9rem;
    max-width: 12rem;
  }
  .settings-retention__field input:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }
  .settings-retention__field-error {
    color: var(--status-warning);
  }
  .settings-retention__actions {
    display: flex;
    align-items: center;
    gap: 0.75rem;
  }
  .settings-retention__save {
    min-height: 40px;
    padding: 0 1.25rem;
    border-radius: 6px;
    border: 1px solid var(--series-1);
    background: color-mix(in oklab, var(--series-1) 15%, transparent);
    color: var(--series-1);
    font-size: 0.85rem;
    font-weight: 500;
    cursor: pointer;
  }
  .settings-retention__save:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }
  .settings-retention__success {
    color: var(--status-good);
  }
  .settings-retention__save-error {
    color: var(--status-warning);
  }

  .settings-view__row {
    display: grid;
    grid-template-columns: repeat(3, 1fr);
    gap: 0.75rem;
    align-items: start;
  }
  @media (max-width: 47.9375rem) {
    .settings-view__row {
      grid-template-columns: 1fr;
    }
  }
  .settings-footprint {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .settings-footprint__tiles {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: 0.6rem;
  }
  .settings-footprint__caption {
    margin: 0;
  }
  .settings-theme,
  .settings-about {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
  }
  .settings-theme__segmented {
    display: inline-flex;
    align-self: flex-start;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    border-radius: 6px;
    overflow: hidden;
  }
  .settings-theme__segment {
    min-height: 40px;
    padding: 0 0.9rem;
    border: none;
    border-right: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: transparent;
    color: var(--ink-2);
    font-size: 0.82rem;
    cursor: pointer;
  }
  .settings-theme__segment:last-child {
    border-right: none;
  }
  .settings-theme__segment--active {
    background: color-mix(in oklab, var(--series-1) 15%, transparent);
    color: var(--series-1);
    font-weight: 500;
  }
  .settings-about__list {
    display: grid;
    grid-template-columns: auto 1fr;
    gap: 0.35rem 1rem;
    margin: 0;
  }
  .settings-about__list dt {
    color: var(--ink-2);
    font-family: var(--font-mono);
    font-size: 0.78rem;
  }
  .settings-about__list dd {
    margin: 0;
    font-size: 0.85rem;
  }
  .settings-about__link {
    color: var(--series-1);
    font-size: 0.82rem;
    text-decoration: none;
    align-self: flex-start;
  }
  .settings-about__link:hover {
    text-decoration: underline;
  }
</style>
