// Pure logic shared by SourcesBanner.svelte and Settings.svelte's own
// sources list: what a collector source's wire value (Registry.Sources'
// map[string]string, runner.go) means. Kept framework-free so "an
// absent/not-applicable source doesn't banner" is testable without a
// component-rendering harness (this repo has none for Svelte components
// -- see vitest.config.ts's own doc; only pure .ts logic is unit-tested
// today).

// SOURCE_NOT_APPLICABLE mirrors the Go side's collect.NotApplicableSentinel
// (runner.go) exactly -- the fixed wire value Sources() reports for a
// collector whose absence is expected-normal environment (e.g. no
// NVIDIA GPU on this box at all), distinct from both "ok" and a plain
// unavailability Detail string. NvidiaCollector.Probe (nvidia.go) is the
// one place that sets it today (Scott's own report: "I don't have an
// nvidia GPU, so this shouldn't be showing up for me").
export const SOURCE_NOT_APPLICABLE = 'not-applicable';

export type SourceEntry = [name: string, detail: string];

// degradedSources returns every source whose detail is neither "ok" NOR
// the not-applicable sentinel -- SourcesBanner's own definition of
// "worth a hint" (see its doc): a not-applicable source has nothing
// fixable to surface, so it must not appear here even though it also
// isn't literally "ok".
export function degradedSources(sources: Record<string, string>): SourceEntry[] {
  return Object.entries(sources).filter(([, detail]) => detail !== 'ok' && detail !== SOURCE_NOT_APPLICABLE);
}
