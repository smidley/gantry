<!--
  Settings: Sources status, the Access (password) card, the retention
  editor (Task 10's four fields), Gantry's own footprint receipt, the
  theme control, and an About card.
  Retention is the one piece with real server round-trips (GET on
  mount, PUT on save) -- everything else reads straight off the live
  frame or the theme store.
-->
<script>
  import { onMount } from 'svelte';
  import { live } from '../lib/sse.svelte';
  import { theme } from '../lib/theme.svelte';
  import { motion } from '../lib/motion.svelte';
  import { liveRing } from '../lib/livering.svelte';
  import { seriesPointsToRing } from '../lib/livering';
  import { fetchSeries, fetchSettings, fetchVersion, putSettings, fetchWebhookTargets, putWebhookTargets, postAuthPassword, postAuthDisable } from '../lib/api';
  import { auth } from '../lib/auth.svelte';
  import { loginErrorMessage } from '../lib/auth';
  import { fmtBytes, fmtPct } from '../lib/format';
  import { SOURCE_NOT_APPLICABLE } from '../lib/sourceStatus';
  import HealthDot from '../components/HealthDot.svelte';
  import StatTile from '../components/StatTile.svelte';

  const LIVE_WINDOW_SEC = 900;

  // NOT_APPLICABLE_COPY: per-source friendly wording for a
  // SOURCE_NOT_APPLICABLE row (NVIDIA presence gate) -- the raw sentinel
  // string is never shown verbatim. Falls back to a generic sentence for
  // any future source that starts using the sentinel without its own
  // entry here yet.
  const NOT_APPLICABLE_COPY = { nvidia: 'No NVIDIA GPU detected.' };

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
  // MOTION_OPTIONS: on/off force animations regardless of the OS's own
  // prefers-reduced-motion setting -- Scott's own ask, in case that OS
  // setting (never confirmed either way) turns out to be part of why a
  // real list reorder read as a hard swap for him.
  const MOTION_OPTIONS = [
    { key: 'system', label: 'System' },
    { key: 'on', label: 'On' },
    { key: 'off', label: 'Off' },
  ];

  let sources = $derived(live.frame?.sources ?? {});
  let sourceNames = $derived(Object.keys(sources).sort());
  let unraidVersion = $derived(live.frame?.unraid_version ?? '');

  let cpuRing = liveRing((f) => f.host?.['gantry.cpu_pct']);
  let rssRing = liveRing((f) => f.host?.['gantry.rss_bytes']);
  let cpuPct = $derived(live.frame?.host?.['gantry.cpu_pct']);
  let rssBytes = $derived(live.frame?.host?.['gantry.rss_bytes']);

  // Seed both footprint sparklines from server history on mount, once --
  // same treatment as every other live ring in this app (ContainerDetail/
  // GPUEntityCard/Overview/Containers): each is a single fixed host
  // metric, fetched straight by name, no discovery needed (unlike
  // Overview's net/io tiles, gantry's own cpu%/rss have no per-device
  // dimension to sum over). A failed/empty seed leaves both rings exactly
  // as unseeded as they are today -- no error state, no fabricated
  // padding.
  onMount(() => {
    const controller = new AbortController();
    const to = Math.floor(Date.now() / 1000);
    const from = to - LIVE_WINDOW_SEC;
    fetchSeries({
      kind: 'host',
      entity: '',
      metrics: ['gantry.cpu_pct', 'gantry.rss_bytes'],
      from,
      to,
      signal: controller.signal,
    })
      .then((results) => {
        const byMetric = {};
        for (const r of results) byMetric[r.metric] = r.points;
        cpuRing.seed(seriesPointsToRing(byMetric['gantry.cpu_pct'] ?? []));
        rssRing.seed(seriesPointsToRing(byMetric['gantry.rss_bytes'] ?? []));
      })
      .catch((err) => {
        if (err?.name === 'AbortError') return; // unmounted before the seed resolved
      });
    return () => controller.abort();
  });

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

  // --- Webhook targets (Task 11/12: not built anywhere in the plan's own
  // Tasks 9-12 file lists, but GET/PUT /api/alerts/webhooks (Task 7/8)
  // has no other UI home either -- this lives here, alongside retention,
  // as "editable integration config," rather than on the Alerts page
  // with the alerting-domain rule editor. header_value is write-only
  // end to end: GET never returns it (header_set stands in for it), and
  // this editor never asks the user to re-type it just to keep it --
  // the field starts blank and blank-on-submit means "leave the stored
  // secret alone," matching webhookTargetInput's own contract server-side. ---
  let webhookTargets = $state([]);
  let webhookLoaded = $state(false);
  let webhookLoadError = $state(null);
  let webhookSaving = $state(false);
  let webhookSaveError = $state(null);
  let webhookEditingId = $state(null); // a target id, '__new__', or null

  const NEW_WEBHOOK_TARGET = { id: '', name: '', url: '', enabled: true, header_name: '', header_set: false, timeout_s: 10 };

  async function loadWebhookTargets() {
    try {
      const resp = await fetchWebhookTargets();
      webhookTargets = resp.targets;
      webhookLoadError = null;
    } catch {
      webhookLoadError = "Couldn't load webhook targets.";
    } finally {
      webhookLoaded = true;
    }
  }

  // saveWebhookTargets sends the whole document back (the PUT /api/
  // alerts/webhooks contract): `edits` is a Map from target id to the
  // form's own pending id/name/url/enabled/header_name/timeout_s/
  // headerValueInput/clearHeader, applied on top of the CURRENT target
  // for that id (or a brand-new entry) so a field this editor didn't
  // touch is never accidentally reset.
  async function saveWebhookTarget(form, isNew) {
    webhookSaving = true;
    webhookSaveError = null;
    const input = {
      id: form.id,
      name: form.name,
      url: form.url,
      enabled: form.enabled,
      header_name: form.header_name,
      timeout_s: form.timeout_s,
    };
    if (form.clearHeader) input.header_value = '';
    else if (form.headerValueInput) input.header_value = form.headerValueInput;
    const nextInputs = webhookTargets
      .filter((t) => t.id !== form.id)
      .map((t) => ({ id: t.id, name: t.name, url: t.url, enabled: t.enabled, header_name: t.header_name ?? '', timeout_s: t.timeout_s }));
    nextInputs.push(input);
    try {
      const resp = await putWebhookTargets(nextInputs);
      webhookTargets = resp.targets;
      webhookEditingId = null;
    } catch (err) {
      webhookSaveError = err instanceof Error ? err.message : String(err);
    } finally {
      webhookSaving = false;
    }
  }

  let webhookForm = $state({ id: '', name: '', url: '', enabled: true, header_name: '', headerValueInput: '', clearHeader: false, timeout_s: 10 });

  function startEditWebhook(target) {
    webhookForm = {
      id: target.id,
      name: target.name,
      url: target.url,
      enabled: target.enabled,
      header_name: target.header_name ?? '',
      headerValueInput: '',
      clearHeader: false,
      timeout_s: target.timeout_s,
    };
    webhookEditingId = target.id;
  }

  function startNewWebhook() {
    webhookForm = { id: '', name: '', url: '', enabled: true, header_name: '', headerValueInput: '', clearHeader: false, timeout_s: 10 };
    webhookEditingId = '__new__';
  }

  function submitWebhookForm(e) {
    e.preventDefault();
    saveWebhookTarget(webhookForm, webhookEditingId === '__new__');
  }

  async function deleteWebhookTarget(id) {
    webhookSaving = true;
    webhookSaveError = null;
    const nextInputs = webhookTargets
      .filter((t) => t.id !== id)
      .map((t) => ({ id: t.id, name: t.name, url: t.url, enabled: t.enabled, header_name: t.header_name ?? '', timeout_s: t.timeout_s }));
    try {
      const resp = await putWebhookTargets(nextInputs);
      webhookTargets = resp.targets;
    } catch (err) {
      webhookSaveError = err instanceof Error ? err.message : String(err);
    } finally {
      webhookSaving = false;
    }
  }

  // --- Access (optional password gate) ----------------------------------
  // The card runs off the auth store (refreshed on mount so it reflects
  // reality, not the boot snapshot). Set/change/disable all round-trip
  // through /api/auth; a change signs out every other session (the
  // server's own contract) and this browser keeps a fresh cookie from
  // the same response, so nothing here ever logs the user out of the
  // tab they're standing in. Passwords live in these fields only for
  // the duration of the request and are never echoed anywhere.
  let pwCurrent = $state('');
  let pwNew = $state('');
  let pwConfirm = $state('');
  let pwSaving = $state(false);
  let pwError = $state(null);
  let pwSuccess = $state(null);

  let disableArmed = $state(false);
  let disableCurrent = $state('');
  let disableBusy = $state(false);
  let disableError = $state(null);

  async function submitPassword(e) {
    e.preventDefault();
    pwError = null;
    pwSuccess = null;
    if (pwNew.length < 8) {
      pwError = 'Password must be at least 8 characters.';
      return;
    }
    if (pwNew !== pwConfirm) {
      pwError = "Passwords don't match.";
      return;
    }
    const firstSet = !auth.passwordSet;
    pwSaving = true;
    try {
      await postAuthPassword(pwCurrent, pwNew);
      pwSuccess = firstSet
        ? 'Password set. This browser stays signed in.'
        : 'Password changed. Every other session was signed out.';
      pwCurrent = '';
      pwNew = '';
      pwConfirm = '';
      await auth.refresh();
    } catch (err) {
      pwError = loginErrorMessage(err);
    } finally {
      pwSaving = false;
    }
  }

  async function submitDisable(e) {
    e.preventDefault();
    disableError = null;
    disableBusy = true;
    try {
      await postAuthDisable(disableCurrent);
      disableArmed = false;
      disableCurrent = '';
      await auth.refresh();
    } catch (err) {
      disableError = loginErrorMessage(err);
    } finally {
      disableBusy = false;
    }
  }

  function logout() {
    // App's own gate effect tears down the SSE connection and swaps to
    // the login screen the moment authenticated flips.
    auth.logout();
  }

  // --- About -----------------------------------------------------------
  let version = $state(null);
  onMount(() => {
    loadSettings();
    loadWebhookTargets();
    auth.refresh();
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
        {@const notApplicable = detail === SOURCE_NOT_APPLICABLE}
        <li class="settings-sources__row">
          <HealthDot status={ok || notApplicable ? 'good' : 'warning'} label={name} />
          {#if !ok}
            <span class="settings-sources__detail">
              {notApplicable ? (NOT_APPLICABLE_COPY[name] ?? 'Not applicable on this system.') : detail}
              {#if name === 'pressure'}
                <a
                  class="settings-sources__learn-more"
                  href="https://github.com/smidley/gantry/blob/main/docs/psi.md"
                  target="_blank"
                  rel="noopener"
                >
                  Learn more &rarr;
                </a>
              {/if}
            </span>
          {/if}
        </li>
      {/each}
    </ul>
  </div>

  <div class="card settings-access">
    <span class="microlabel">Access</span>
    {#if auth.mode === 'proxy'}
      <p class="settings-access__note">
        Authentication is handled by your reverse proxy (GANTRY_AUTH=proxy). Gantry's built-in password gate is off,
        and password settings are managed at the proxy, not here.
      </p>
    {:else}
      {#if auth.showsNudge}
        <p class="settings-access__nudge">
          No password set — anyone on your network can view and manage this server.
        </p>
      {:else}
        <p class="settings-access__note">Password protection is on. Signing in lasts 7 days per visit, 30 days at most.</p>
        {#if auth.envManaged}
          <p class="settings-access__note settings-access__note--env">
            The password comes from the GANTRY_PASSWORD container variable at every start — a change made here lasts
            only until the next restart re-applies it. Update the variable in the container template to make a change
            stick. Removing the variable does <em>not</em> turn the password off; only “Turn off password” below does.
          </p>
        {/if}
      {/if}

      <form class="settings-access__form" onsubmit={submitPassword} novalidate>
        {#if auth.passwordSet}
          <label class="settings-access__field">
            <span class="microlabel">Current password</span>
            <input type="password" bind:value={pwCurrent} autocomplete="current-password" disabled={pwSaving} />
          </label>
        {/if}
        <label class="settings-access__field">
          <span class="microlabel">New password</span>
          <input type="password" bind:value={pwNew} autocomplete="new-password" disabled={pwSaving} />
        </label>
        <label class="settings-access__field">
          <span class="microlabel">Confirm new password</span>
          <input type="password" bind:value={pwConfirm} autocomplete="new-password" disabled={pwSaving} />
        </label>
        <div class="settings-access__actions">
          <button type="submit" class="settings-access__save" disabled={pwSaving}>
            {pwSaving ? 'Saving…' : auth.passwordSet ? 'Change password' : 'Set password'}
          </button>
          {#if auth.passwordSet}
            <span class="microlabel">Changing the password signs out every other session.</span>
          {/if}
        </div>
        {#if pwError}<p class="microlabel settings-access__error" role="alert">{pwError}</p>{/if}
        {#if pwSuccess}<p class="microlabel settings-access__success">{pwSuccess}</p>{/if}
      </form>

      {#if auth.passwordSet}
        <div class="settings-access__session-row">
          <button type="button" class="settings-access__secondary" onclick={logout}>Log out</button>
          {#if !disableArmed}
            <button type="button" class="settings-access__secondary" onclick={() => (disableArmed = true)}>
              Turn off password…
            </button>
          {/if}
        </div>
        {#if disableArmed}
          <form class="settings-access__disable" onsubmit={submitDisable} novalidate>
            <p class="settings-access__note">
              Turning the password off reopens this dashboard to anyone on your network and signs out every session.
              Enter the current password to confirm.
            </p>
            <div class="settings-access__actions">
              <label class="settings-access__field">
                <span class="microlabel">Current password</span>
                <input type="password" bind:value={disableCurrent} autocomplete="current-password" disabled={disableBusy} />
              </label>
              <button type="submit" class="settings-access__danger" disabled={disableBusy}>
                {disableBusy ? 'Turning off…' : 'Turn off password'}
              </button>
              <button
                type="button"
                class="settings-access__secondary"
                onclick={() => {
                  disableArmed = false;
                  disableCurrent = '';
                  disableError = null;
                }}
                disabled={disableBusy}
              >
                Cancel
              </button>
            </div>
            {#if disableError}<p class="microlabel settings-access__error" role="alert">{disableError}</p>{/if}
          </form>
        {/if}
      {/if}
    {/if}
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

  <div class="card settings-webhooks">
    <span class="microlabel">Webhook targets</span>
    {#if webhookLoadError}
      <p class="microlabel settings-webhooks__error">{webhookLoadError}</p>
    {:else if !webhookLoaded}
      <p class="microlabel">Loading…</p>
    {:else}
      {#if webhookTargets.length === 0}
        <p class="microlabel settings-webhooks__empty">No webhook targets configured.</p>
      {:else}
        <ul class="settings-webhooks__list">
          {#each webhookTargets as t (t.id)}
            <li class="settings-webhooks__row-wrap" data-target-id={t.id}>
              {#if webhookEditingId === t.id}
                <!-- novalidate: same trap as the retention form's own doc above --
                     a real min/max attr (timeout_s) would otherwise silently
                     block submit for an out-of-range value with no feedback
                     at all, before the server ever gets a chance to answer. -->
                <form class="settings-webhooks__form" onsubmit={submitWebhookForm} novalidate>
                  <div class="settings-webhooks__form-row">
                    <label class="settings-webhooks__field">
                      <span class="microlabel">Name</span>
                      <input type="text" bind:value={webhookForm.name} disabled={t.env_overridden} />
                    </label>
                    <label class="settings-webhooks__field settings-webhooks__field--wide">
                      <span class="microlabel">URL</span>
                      <input type="text" bind:value={webhookForm.url} disabled={t.env_overridden} />
                    </label>
                  </div>
                  <div class="settings-webhooks__form-row">
                    <label class="settings-webhooks__field">
                      <span class="microlabel">Header name</span>
                      <input type="text" bind:value={webhookForm.header_name} placeholder="e.g. Authorization" />
                    </label>
                    <label class="settings-webhooks__field">
                      <span class="microlabel">Header value</span>
                      <input
                        type="password"
                        bind:value={webhookForm.headerValueInput}
                        disabled={webhookForm.clearHeader}
                        placeholder={t.header_set ? 'Leave blank to keep the current secret' : 'No secret set'}
                      />
                    </label>
                    <label class="settings-webhooks__clear">
                      <input type="checkbox" bind:checked={webhookForm.clearHeader} disabled={!t.header_set} />
                      <span class="microlabel">Clear stored secret</span>
                    </label>
                    <label class="settings-webhooks__field">
                      <span class="microlabel">Timeout (s)</span>
                      <input type="number" min="1" max="30" bind:value={webhookForm.timeout_s} />
                    </label>
                  </div>
                  <label class="settings-webhooks__enabled">
                    <input type="checkbox" bind:checked={webhookForm.enabled} disabled={t.env_overridden} />
                    <span>Enabled</span>
                  </label>
                  {#if t.env_overridden}
                    <p class="microlabel settings-webhooks__env-note">
                      URL/enabled/timeout are set by GANTRY_WEBHOOK_URL and can't be changed here.
                    </p>
                  {/if}
                  <div class="settings-webhooks__actions">
                    <button type="submit" class="settings-webhooks__save" disabled={webhookSaving}>
                      {webhookSaving ? 'Saving…' : 'Save'}
                    </button>
                    <button type="button" class="settings-webhooks__cancel" onclick={() => (webhookEditingId = null)} disabled={webhookSaving}>
                      Cancel
                    </button>
                  </div>
                </form>
              {:else}
                <div class="settings-webhooks__row">
                  <HealthDot status={t.enabled ? 'good' : 'warning'} label={t.name || t.id} />
                  <span class="settings-webhooks__url">{t.url}</span>
                  <span class="microlabel settings-webhooks__secret-state">
                    {t.header_set ? 'Secret set' : 'No secret'}
                  </span>
                  {#if t.env_overridden}<span class="settings-webhooks__lock">Env locked</span>{/if}
                  <div class="settings-webhooks__row-actions">
                    <button type="button" onclick={() => startEditWebhook(t)}>Edit</button>
                    {#if !t.env_overridden}
                      <button type="button" onclick={() => deleteWebhookTarget(t.id)}>Delete</button>
                    {/if}
                  </div>
                </div>
              {/if}
            </li>
          {/each}
        </ul>
      {/if}

      {#if webhookEditingId === '__new__'}
        <!-- novalidate: same trap as the retention form's own doc above. -->
        <form class="settings-webhooks__form" onsubmit={submitWebhookForm} novalidate>
          <div class="settings-webhooks__form-row">
            <label class="settings-webhooks__field">
              <span class="microlabel">Target ID</span>
              <input type="text" bind:value={webhookForm.id} placeholder="e.g. home-assistant" />
            </label>
            <label class="settings-webhooks__field">
              <span class="microlabel">Name</span>
              <input type="text" bind:value={webhookForm.name} />
            </label>
            <label class="settings-webhooks__field settings-webhooks__field--wide">
              <span class="microlabel">URL</span>
              <input type="text" bind:value={webhookForm.url} placeholder="https://…" />
            </label>
          </div>
          <div class="settings-webhooks__form-row">
            <label class="settings-webhooks__field">
              <span class="microlabel">Header name</span>
              <input type="text" bind:value={webhookForm.header_name} placeholder="e.g. Authorization" />
            </label>
            <label class="settings-webhooks__field">
              <span class="microlabel">Header value</span>
              <input type="password" bind:value={webhookForm.headerValueInput} placeholder="optional" />
            </label>
            <label class="settings-webhooks__field">
              <span class="microlabel">Timeout (s)</span>
              <input type="number" min="1" max="30" bind:value={webhookForm.timeout_s} />
            </label>
          </div>
          <div class="settings-webhooks__actions">
            <button type="submit" class="settings-webhooks__save" disabled={webhookSaving}>
              {webhookSaving ? 'Saving…' : 'Add target'}
            </button>
            <button type="button" class="settings-webhooks__cancel" onclick={() => (webhookEditingId = null)} disabled={webhookSaving}>
              Cancel
            </button>
          </div>
        </form>
      {:else}
        <button type="button" class="settings-webhooks__add" onclick={startNewWebhook}>Add webhook target</button>
      {/if}

      {#if webhookSaveError}<p class="microlabel settings-webhooks__error">{webhookSaveError}</p>{/if}
    {/if}
  </div>

  <div class="settings-view__row">
    <div class="card settings-footprint">
      <span class="microlabel">Gantry footprint</span>
      <div class="settings-footprint__tiles">
        <StatTile bare label="CPU" liveValue={cpuPct ?? 0} formatValue={fmtPct} sparklinePoints={cpuRing.points} />
        <StatTile bare label="Memory" liveValue={rssBytes ?? 0} formatValue={fmtBytes} sparklinePoints={rssRing.points} />
      </div>
      <p class="microlabel settings-footprint__caption">Budget: core &le;2% &middot; RSS &le;100MB</p>
    </div>

    <div class="card settings-theme">
      <span class="microlabel">Theme</span>
      <div class="segmented" role="group" aria-label="Theme">
        {#each THEME_OPTIONS as opt (opt.key)}
          <button
            type="button"
            class="segmented__btn"
            class:segmented__btn--active={theme.preference === opt.key}
            onclick={() => theme.set(opt.key)}
          >
            {opt.label}
          </button>
        {/each}
      </div>

      <span class="microlabel">Animations</span>
      <div class="segmented" role="group" aria-label="Animations">
        {#each MOTION_OPTIONS as opt (opt.key)}
          <button
            type="button"
            class="segmented__btn"
            class:segmented__btn--active={motion.preference === opt.key}
            onclick={() => motion.set(opt.key)}
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
  .settings-sources__learn-more {
    margin-left: 0.4em;
    color: var(--series-1);
    text-decoration: none;
    white-space: nowrap;
  }
  .settings-sources__learn-more:hover {
    text-decoration: underline;
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

  .settings-access {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
  }
  .settings-access__nudge {
    margin: 0;
    font-size: 0.85rem;
    color: var(--status-warning);
  }
  .settings-access__note {
    margin: 0;
    font-size: 0.82rem;
    color: var(--ink-2);
  }
  .settings-access__note--env {
    padding: 0.5rem 0.6rem;
    border-radius: 8px;
    background: color-mix(in oklab, var(--status-warning) 10%, transparent);
    color: var(--ink);
  }
  .settings-access__form,
  .settings-access__disable {
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
  }
  .settings-access__field {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    max-width: 18rem;
  }
  .settings-access__field input {
    min-height: 40px;
    padding: 0 0.75rem;
    border-radius: 6px;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: var(--surface);
    color: var(--ink);
    font-size: 0.9rem;
  }
  .settings-access__field input:disabled {
    opacity: 0.6;
  }
  .settings-access__actions {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    flex-wrap: wrap;
  }
  .settings-access__save {
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
  .settings-access__secondary {
    min-height: 40px;
    padding: 0 0.9rem;
    border-radius: 6px;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: transparent;
    color: var(--ink);
    font-size: 0.8rem;
    cursor: pointer;
  }
  .settings-access__danger {
    min-height: 40px;
    padding: 0 1rem;
    border-radius: 6px;
    border: 1px solid var(--status-warning);
    background: color-mix(in oklab, var(--status-warning) 12%, transparent);
    color: var(--status-warning);
    font-size: 0.85rem;
    font-weight: 500;
    cursor: pointer;
  }
  .settings-access__save:disabled,
  .settings-access__secondary:disabled,
  .settings-access__danger:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }
  .settings-access__session-row {
    display: flex;
    gap: 0.6rem;
  }
  .settings-access__error {
    color: var(--status-warning);
    margin: 0;
  }
  .settings-access__success {
    color: var(--status-good);
    margin: 0;
  }

  .settings-webhooks {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
  }
  .settings-webhooks__error {
    color: var(--status-warning);
    margin: 0;
  }
  .settings-webhooks__empty {
    margin: 0;
    color: var(--ink-2);
  }
  .settings-webhooks__list {
    margin: 0;
    padding: 0;
    list-style: none;
    display: flex;
    flex-direction: column;
  }
  .settings-webhooks__row-wrap {
    padding: 0.5rem 0;
    border-bottom: 1px solid color-mix(in oklab, var(--ink) 8%, transparent);
  }
  .settings-webhooks__row {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    flex-wrap: wrap;
  }
  .settings-webhooks__url {
    font-family: var(--font-mono);
    font-size: 0.78rem;
    color: var(--ink-2);
    overflow-wrap: anywhere;
    flex: 1;
    min-width: 10rem;
  }
  .settings-webhooks__secret-state {
    white-space: nowrap;
  }
  .settings-webhooks__lock {
    font-family: var(--font-mono);
    font-size: 0.65rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    padding: 0.1rem 0.4rem;
    border-radius: 999px;
    background: color-mix(in oklab, var(--ink) 8%, transparent);
    color: var(--ink-2);
    white-space: nowrap;
  }
  .settings-webhooks__row-actions {
    display: flex;
    gap: 0.5rem;
  }
  .settings-webhooks__row-actions button,
  .settings-webhooks__add {
    min-height: 40px;
    padding: 0 0.75rem;
    border-radius: 6px;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: transparent;
    color: var(--ink);
    font-size: 0.8rem;
    cursor: pointer;
  }
  .settings-webhooks__add {
    align-self: flex-start;
  }
  .settings-webhooks__form {
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
    padding: 0.6rem;
    border-radius: 8px;
    background: color-mix(in oklab, var(--ink) 4%, transparent);
  }
  .settings-webhooks__form-row {
    display: flex;
    flex-wrap: wrap;
    gap: 0.6rem;
  }
  .settings-webhooks__field {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    flex: 1 1 8rem;
    min-width: 8rem;
  }
  .settings-webhooks__field--wide {
    flex-basis: 100%;
  }
  .settings-webhooks__field input {
    min-height: 40px;
    padding: 0 0.6rem;
    border-radius: 6px;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: var(--surface);
    color: var(--ink);
    font-size: 0.85rem;
  }
  .settings-webhooks__field input:disabled {
    opacity: 0.6;
  }
  .settings-webhooks__clear {
    display: flex;
    align-items: center;
    gap: 0.4rem;
  }
  .settings-webhooks__enabled {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.85rem;
  }
  .settings-webhooks__env-note {
    margin: 0;
    color: var(--ink-2);
  }
  .settings-webhooks__actions {
    display: flex;
    gap: 0.6rem;
  }
  .settings-webhooks__save,
  .settings-webhooks__cancel {
    min-height: 40px;
    padding: 0 1.1rem;
    border-radius: 6px;
    font-size: 0.85rem;
    cursor: pointer;
  }
  .settings-webhooks__save {
    border: 1px solid var(--series-1);
    background: color-mix(in oklab, var(--series-1) 15%, transparent);
    color: var(--series-1);
    font-weight: 500;
  }
  .settings-webhooks__cancel {
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: transparent;
    color: var(--ink);
  }
  .settings-webhooks__save:disabled,
  .settings-webhooks__cancel:disabled {
    opacity: 0.6;
    cursor: not-allowed;
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
  /* min-width:0 -- same released-minimum treatment as ContainerDetail's
     chart cards (see that file's own longer doc): the tiles' sparkline
     canvases bake their width in literal pixels, and this card's
     default min-width:auto would otherwise pin its 1fr track at that
     stale width when the row narrows, shoving the theme/about cards
     past the viewport instead of letting Sparkline's own
     ResizeObserver re-fit the canvas (reproduced at 1920 -> 1200:
     first track stuck at 550px, About's right edge 16px past the
     page). */
  .settings-footprint {
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    min-width: 0;
  }
  /* bare rail rows (see StatTile's own doc), same instrument-rail
     treatment as Overview's metrics rail -- a hairline between CPU/
     Memory, not two separate stat cards side by side. */
  .settings-footprint__tiles {
    display: flex;
    flex-direction: column;
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
  .settings-theme .segmented {
    align-self: flex-start;
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
