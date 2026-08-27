import { describe, expect, it } from 'vitest';
import { stripAnsi } from './ansi';

describe('stripAnsi', () => {
  it('strips a single color code', () => {
    expect(stripAnsi('\x1b[31mred text\x1b[0m')).toBe('red text');
  });

  it('strips multiple codes in one string', () => {
    expect(stripAnsi('\x1b[1m\x1b[32mbold green\x1b[0m plain')).toBe('bold green plain');
  });

  it('strips multi-parameter SGR codes (semicolon-separated)', () => {
    expect(stripAnsi('\x1b[38;5;196mbright red\x1b[0m')).toBe('bright red');
  });

  it('leaves plain text with no escape codes untouched', () => {
    expect(stripAnsi('just a plain log line')).toBe('just a plain log line');
  });

  it('handles an empty string', () => {
    expect(stripAnsi('')).toBe('');
  });
});
