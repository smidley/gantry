<!--
  RuleEditor: the inline editing form for one alert rule (Task 11),
  rendered by Alerts.svelte in place of a rule's plain row while it's
  being edited or created. Editing a BUILTIN edits its numbers, never
  its identity -- id/type/kind/metric/band_family are fixed, read-only
  facts here, never inputs; threshold/warn/critical/clear/for/clear-for/
  severity/renotify/enabled all are. Creating a user rule is threshold-
  only (v1 -- see this file's own id/kind/metric inputs, which only
  render in that mode) and additionally asks for the identity fields a
  builtin already has fixed.

  Validation here mirrors internal/alert/rule.go's ValidateRule for
  immediate feedback; the server is still the final word (Alerts.svelte
  surfaces its own 400 message as `serverError` when a save is rejected
  for a reason this local check didn't catch).
-->
<script>
  let {
    rule,
    isNew = false,
    saving = false,
    serverError = null,
    onSave,
    onCancel,
  } = $props();

  const KIND_OPTIONS = ['host', 'container', 'disk', 'gpu', 'unraid'];
  const SEVERITY_OPTIONS = ['info', 'warning', 'alert'];
  const CLASS_OPTIONS = [
    { value: '', label: 'Any' },
    { value: 'nvme', label: 'NVMe only' },
    { value: '!nvme', label: 'Non-NVMe only' },
  ];

  // seed: a deliberate one-time snapshot of `rule` into this form's own
  // local editable state -- every RuleEditor instance is freshly
  // mounted per edit session (Alerts.svelte's own {#if editingId ===
  // rule.id}), so there is no later `rule` change this form would ever
  // need to track. Reading through this plain local alias, rather than
  // the `rule` prop binding directly, is what it takes to seed $state
  // from a prop on purpose without Svelte's state_referenced_locally
  // warning firing for every field (that warning exists for the far
  // more common case of ACCIDENTALLY forgetting a prop can change).
  const seed = rule;

  let name = $state(seed.name);
  let enabled = $state(seed.enabled);
  let kind = $state(seed.kind || 'host');
  let entityGlob = $state(seed.entity_glob || '*');
  let entityClass = $state(seed.entity_class || '');
  let metric = $state(seed.metric || '');
  let op = $state(seed.op || '>');
  let threshold = $state(seed.threshold ?? 0);
  let clearThreshold = $state(seed.clear_threshold ?? 0);
  let warnThreshold = $state(seed.warn_threshold ?? 0);
  let criticalThreshold = $state(seed.critical_threshold ?? 0);
  let forSeconds = $state(seed.for_seconds ?? 300);
  let clearSeconds = $state(seed.clear_seconds ?? 300);
  let severity = $state(seed.severity || 'warning');
  let renotifyHours = $state(seed.renotify_hours ?? 0);

  // id: new-rule-only -- auto-slugified from the name field until the
  // user directly edits the id input themselves (idTouched), the same
  // "auto-fill until overridden" idiom most slug-generating forms use.
  let id = $state(seed.id || '');
  let idTouched = $state(false);
  $effect(() => {
    if (isNew && !idTouched) {
      id = name
        .trim()
        .toLowerCase()
        .replace(/[^a-z0-9]+/g, '-')
        .replace(/(^-+|-+$)/g, '');
    }
  });

  let fieldErrors = $state({});

  // validate mirrors ValidateRule's own checks that a form can usefully
  // catch before a round trip -- band_family/entity syntax/duplicate-id
  // checks are left to the server, which has the full current rule list
  // to check against.
  function validate() {
    const errs = {};
    if (!name.trim()) errs.name = 'Name is required.';
    if (isNew && !/^[a-z0-9]+(-[a-z0-9]+)*$/.test(id)) {
      errs.id = 'Use lowercase letters, numbers, and hyphens only.';
    }
    if (isNew && !metric.trim()) errs.metric = 'Metric is required.';
    if (forSeconds < 0 || forSeconds > 3600) {
      errs.for_seconds = 'Must be between 0 and 3600 seconds.';
    }
    if (renotifyHours < 0 || renotifyHours > 168) {
      errs.renotify_hours = 'Must be between 0 and 168 hours.';
    }
    if (threshold === clearThreshold && clearSeconds > 0) {
      errs.clear_threshold = 'Threshold and clear threshold must differ when clear seconds is set.';
    }
    return errs;
  }

  function submit(e) {
    e.preventDefault();
    const errs = validate();
    if (Object.keys(errs).length > 0) {
      fieldErrors = errs;
      return;
    }
    fieldErrors = {};
    onSave({
      ...rule,
      name: name.trim(),
      enabled,
      ...(isNew ? { id, kind, entity_glob: entityGlob, entity_class: entityClass, metric: metric.trim(), op } : {}),
      threshold,
      clear_threshold: clearThreshold,
      warn_threshold: warnThreshold,
      critical_threshold: criticalThreshold,
      for_seconds: forSeconds,
      clear_seconds: clearSeconds,
      severity,
      renotify_hours: renotifyHours,
    });
  }
</script>

<!-- novalidate: min/max stay as real DOM attributes (a11y + range hint),
     but native constraint validation would otherwise silently block the
     submit event for an out-of-range value before this file's own
     validate() ever runs -- the exact trap Settings.svelte's retention
     form already documents and guards against; reproduced live here
     too (typing 99999 into a max=3600 field produced no request, no
     error, nothing -- just a silently-blocked submit). This app's own
     styled inline error (validate(), above) is the only feedback for
     every failure mode instead. -->
<form class="rule-editor" onsubmit={submit} novalidate>
  {#if isNew}
    <div class="rule-editor__row">
      <label class="rule-editor__field">
        <span class="microlabel">Rule ID</span>
        <input
          type="text"
          bind:value={id}
          oninput={() => (idTouched = true)}
          placeholder="my-custom-rule"
        />
        {#if fieldErrors.id}<span class="microlabel rule-editor__field-error">{fieldErrors.id}</span>{/if}
      </label>
      <label class="rule-editor__field">
        <span class="microlabel">Kind</span>
        <select bind:value={kind}>
          {#each KIND_OPTIONS as k (k)}<option value={k}>{k}</option>{/each}
        </select>
      </label>
      <label class="rule-editor__field">
        <span class="microlabel">Entity</span>
        <input type="text" bind:value={entityGlob} placeholder="* (any) or a name" />
      </label>
      <label class="rule-editor__field">
        <span class="microlabel">Disk class</span>
        <select bind:value={entityClass}>
          {#each CLASS_OPTIONS as c (c.value)}<option value={c.value}>{c.label}</option>{/each}
        </select>
      </label>
    </div>
    <div class="rule-editor__row">
      <label class="rule-editor__field">
        <span class="microlabel">Metric</span>
        <input type="text" bind:value={metric} placeholder="e.g. cpu.total" />
        {#if fieldErrors.metric}<span class="microlabel rule-editor__field-error">{fieldErrors.metric}</span>{/if}
      </label>
      <label class="rule-editor__field">
        <span class="microlabel">Comparison</span>
        <select bind:value={op}>
          <option value=">">goes over</option>
          <option value="<">drops below</option>
        </select>
      </label>
    </div>
  {/if}

  <label class="rule-editor__field rule-editor__field--wide">
    <span class="microlabel">Name</span>
    <input type="text" bind:value={name} />
    {#if fieldErrors.name}<span class="microlabel rule-editor__field-error">{fieldErrors.name}</span>{/if}
  </label>

  <div class="rule-editor__row">
    <label class="rule-editor__field">
      <span class="microlabel">Threshold (fire)</span>
      <input type="number" step="any" bind:value={threshold} />
    </label>
    <label class="rule-editor__field">
      <span class="microlabel">Clear threshold</span>
      <input type="number" step="any" bind:value={clearThreshold} />
      {#if fieldErrors.clear_threshold}<span class="microlabel rule-editor__field-error">{fieldErrors.clear_threshold}</span>{/if}
    </label>
    <label class="rule-editor__field">
      <span class="microlabel">Warn threshold</span>
      <input type="number" step="any" bind:value={warnThreshold} />
    </label>
    <label class="rule-editor__field">
      <span class="microlabel">Critical threshold</span>
      <input type="number" step="any" bind:value={criticalThreshold} />
    </label>
  </div>

  <div class="rule-editor__row">
    <label class="rule-editor__field">
      <span class="microlabel">Sustained for (seconds)</span>
      <input type="number" min="0" max="3600" bind:value={forSeconds} />
      {#if fieldErrors.for_seconds}<span class="microlabel rule-editor__field-error">{fieldErrors.for_seconds}</span>{/if}
    </label>
    <label class="rule-editor__field">
      <span class="microlabel">Clear for (seconds)</span>
      <input type="number" min="0" bind:value={clearSeconds} />
    </label>
    <label class="rule-editor__field">
      <span class="microlabel">Severity</span>
      <select bind:value={severity}>
        {#each SEVERITY_OPTIONS as s (s)}<option value={s}>{s}</option>{/each}
      </select>
    </label>
    <label class="rule-editor__field">
      <span class="microlabel">Re-notify (hours, 0=off)</span>
      <input type="number" min="0" max="168" bind:value={renotifyHours} />
      {#if fieldErrors.renotify_hours}<span class="microlabel rule-editor__field-error">{fieldErrors.renotify_hours}</span>{/if}
    </label>
  </div>

  <label class="rule-editor__enabled">
    <input type="checkbox" bind:checked={enabled} />
    <span>Enabled</span>
  </label>

  {#if serverError}<p class="rule-editor__server-error">{serverError}</p>{/if}

  <div class="rule-editor__actions">
    <button type="submit" class="rule-editor__save" disabled={saving}>{saving ? 'Saving…' : 'Save'}</button>
    <button type="button" class="rule-editor__cancel" onclick={onCancel} disabled={saving}>Cancel</button>
  </div>
</form>

<style>
  .rule-editor {
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
    padding: 0.75rem;
    border-radius: 8px;
    background: color-mix(in oklab, var(--ink) 4%, transparent);
    border: 1px solid color-mix(in oklab, var(--ink) 10%, transparent);
  }
  .rule-editor__row {
    display: flex;
    flex-wrap: wrap;
    gap: 0.75rem;
  }
  .rule-editor__field {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    min-width: 8rem;
    flex: 1 1 8rem;
  }
  .rule-editor__field--wide {
    flex-basis: 100%;
  }
  .rule-editor__field input,
  .rule-editor__field select {
    min-height: 40px;
    padding: 0 0.6rem;
    border-radius: 6px;
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: var(--surface);
    color: var(--ink);
    font-size: 0.85rem;
  }
  .rule-editor__field-error {
    color: var(--status-warning);
  }
  .rule-editor__enabled {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.85rem;
  }
  .rule-editor__enabled input {
    width: 16px;
    height: 16px;
  }
  .rule-editor__server-error {
    margin: 0;
    font-size: 0.82rem;
    color: var(--status-critical);
  }
  .rule-editor__actions {
    display: flex;
    gap: 0.6rem;
  }
  .rule-editor__save,
  .rule-editor__cancel {
    min-height: 40px;
    padding: 0 1.1rem;
    border-radius: 6px;
    font-size: 0.85rem;
    cursor: pointer;
  }
  .rule-editor__save {
    border: 1px solid var(--series-1);
    background: color-mix(in oklab, var(--series-1) 15%, transparent);
    color: var(--series-1);
    font-weight: 500;
  }
  .rule-editor__cancel {
    border: 1px solid color-mix(in oklab, var(--ink) 15%, transparent);
    background: transparent;
    color: var(--ink);
  }
  .rule-editor__save:disabled,
  .rule-editor__cancel:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }
</style>
