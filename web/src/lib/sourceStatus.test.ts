import { describe, expect, it } from 'vitest';
import { degradedSources, SOURCE_NOT_APPLICABLE } from './sourceStatus';

describe('degradedSources', () => {
  it('excludes every "ok" source', () => {
    expect(degradedSources({ docker: 'ok', host: 'ok' })).toEqual([]);
  });

  it('includes a source with a plain unavailability detail', () => {
    expect(degradedSources({ docker: 'ok', pressure: 'PSI disabled' })).toEqual([['pressure', 'PSI disabled']]);
  });

  // The regression this exists to pin: an absent/not-applicable source
  // (NvidiaCollector's own report when no NVIDIA GPU is on the box at
  // all) must NOT come back from this function -- SourcesBanner renders
  // one card per entry it returns, so a source landing here at all is
  // exactly what "the banner still shows a hint for it" would mean.
  it('does not include a not-applicable source -- the whole point of the sentinel', () => {
    expect(degradedSources({ nvidia: SOURCE_NOT_APPLICABLE })).toEqual([]);
  });

  it('a mix of ok, not-applicable, and genuinely degraded sources keeps only the degraded one', () => {
    const sources = {
      docker: 'ok',
      host: 'ok',
      nvidia: SOURCE_NOT_APPLICABLE,
      pressure: 'PSI disabled — add psi=1 to the syslinux append line to enable',
    };
    expect(degradedSources(sources)).toEqual([['pressure', sources.pressure]]);
  });

  it('returns an empty array for no sources at all', () => {
    expect(degradedSources({})).toEqual([]);
  });
});
