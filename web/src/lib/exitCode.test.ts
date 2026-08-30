import { describe, expect, it } from 'vitest';
import { describeExitCode } from './exitCode';

describe('describeExitCode', () => {
  it('names the common codes plainly', () => {
    expect(describeExitCode(0)).toBe('clean exit');
    expect(describeExitCode(1)).toBe('generic error');
    expect(describeExitCode(126)).toBe('command not executable');
    expect(describeExitCode(127)).toBe('command not found');
    expect(describeExitCode(137)).toBe('killed, likely out of memory');
    expect(describeExitCode(143)).toBe('terminated');
  });

  it('returns empty for an uncommon/unmapped code rather than guessing', () => {
    expect(describeExitCode(2)).toBe('');
    expect(describeExitCode(255)).toBe('');
  });
});
