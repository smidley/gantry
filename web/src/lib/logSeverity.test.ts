import { describe, expect, it } from 'vitest';
import { classifyLogLine, SEVERITY_ORDER } from './logSeverity';

describe('classifyLogLine', () => {
  describe('bracket form', () => {
    it('classifies [ERROR]/[WARN]/[INFO]/[DEBUG]', () => {
      expect(classifyLogLine('[ERROR] connection refused')).toBe('error');
      expect(classifyLogLine('[WARN] retrying in 5s')).toBe('warn');
      expect(classifyLogLine('[INFO] server started')).toBe('info');
      expect(classifyLogLine('[DEBUG] cache miss for key foo')).toBe('debug');
    });

    it('accepts the longer WARNING spelling too', () => {
      expect(classifyLogLine('[WARNING] disk 90% full')).toBe('warn');
    });
  });

  describe('colon form', () => {
    it('classifies ERROR:/WARN:/INFO:/DEBUG: at any case', () => {
      expect(classifyLogLine('ERROR: could not bind to port 8080')).toBe('error');
      expect(classifyLogLine('warn: deprecated flag used')).toBe('warn');
      expect(classifyLogLine('Info: listening on :8380')).toBe('info');
      expect(classifyLogLine('DEBUG: request id abc123')).toBe('debug');
    });
  });

  describe('logfmt form (level=value)', () => {
    it('classifies level=warn and friends', () => {
      expect(classifyLogLine('time=2026-08-28T10:00:00Z level=warn msg="slow query"')).toBe('warn');
      expect(classifyLogLine('level=error msg="panic recovered"')).toBe('error');
      expect(classifyLogLine('level=debug component=cache')).toBe('debug');
    });
  });

  describe('JSON form ("level":"value")', () => {
    it('classifies a JSON-encoded level field', () => {
      expect(classifyLogLine('{"level":"error","msg":"write failed"}')).toBe('error');
      expect(classifyLogLine('{"ts":123,"level":"info","msg":"ready"}')).toBe('info');
      // Tolerant of a space after the colon and single quotes around keys --
      // still a whole "info" word at a boundary either way.
      expect(classifyLogLine("{'level': 'warn', 'msg': 'nearing quota'}")).toBe('warn');
    });
  });

  describe('glog form (single letter + mmdd, line-anchored)', () => {
    it('classifies E0828/W0828/I0828/F0828', () => {
      expect(classifyLogLine('E0828 12:34:56.789012   123 file.go:42] write failed')).toBe('error');
      expect(classifyLogLine('W0828 12:34:56.789012   123 file.go:42] retrying')).toBe('warn');
      expect(classifyLogLine('I0828 12:34:56.789012   123 file.go:42] started')).toBe('info');
      expect(classifyLogLine('F0828 12:34:56.789012   123 file.go:42] unrecoverable')).toBe('error');
    });

    it('is case-insensitive on the glog letter too', () => {
      expect(classifyLogLine('e0101 00:00:00.000000 1 x.go:1] boom')).toBe('error');
    });

    it('only recognizes the glog letter at the very start of the line', () => {
      // "E0828" mid-line is not glog's own convention -- and isn't a whole
      // level WORD either, so this must fall through to 'other', not
      // misfire as an error just because it contains a capital E.
      expect(classifyLogLine('retry attempt E0828 scheduled')).toBe('other');
    });

    it('glog has no distinct debug letter, but a bare "debug" word elsewhere in the same line still classifies', () => {
      expect(classifyLogLine('D0828 12:00:00.000000 1 x.go:1] debug trace point')).toBe('debug');
    });
  });

  describe('bare keyword forms (FATAL/PANIC -> error, NOTICE -> info, TRACE -> debug)', () => {
    it('recognizes these even with no bracket/colon/logfmt wrapper', () => {
      expect(classifyLogLine('FATAL unrecoverable error, shutting down')).toBe('error');
      expect(classifyLogLine('panic: runtime error: index out of range')).toBe('error');
      expect(classifyLogLine('NOTICE: config reloaded')).toBe('info');
      expect(classifyLogLine('TRACE entering handleRequest')).toBe('debug');
    });
  });

  describe('case-insensitivity', () => {
    it('matches regardless of case for every form', () => {
      expect(classifyLogLine('error: lowercase works too')).toBe('error');
      expect(classifyLogLine('Error: mixed case works too')).toBe('error');
      expect(classifyLogLine('ErRoR: silly case works too')).toBe('error');
    });
  });

  describe('first-match-wins on the earliest token', () => {
    it('picks the token that occurs first in the line, not the last', () => {
      // "info" (a structured colon-form marker) appears before "fatal"
      // (a bare word in the message body) -- the structured, EARLIER
      // marker must win.
      expect(classifyLogLine('INFO: saw a fatal exception in a downstream service, continuing')).toBe('info');
    });

    it('a glog prefix earlier than a word-form marker elsewhere still wins by position', () => {
      expect(classifyLogLine('W0828 12:00:00 1 x.go:1] level=error downstream, degrading gracefully')).toBe('warn');
    });
  });

  describe('ANSI escape codes', () => {
    it('is not confused by SGR color codes wrapping the level marker', () => {
      const line = '\x1b[31m[ERROR]\x1b[0m connection refused';
      expect(classifyLogLine(line)).toBe('error');
    });

    it('still finds a glog prefix even when ANSI codes precede it at the start of the line', () => {
      const line = '\x1b[33mW0828 12:00:00 1 x.go:1] retrying\x1b[0m';
      expect(classifyLogLine(line)).toBe('warn');
    });

    it('is not confused by ANSI codes wrapping a bare colon-form marker', () => {
      const line = 'plain \x1b[36mDEBUG:\x1b[0m cache warm';
      expect(classifyLogLine(line)).toBe('debug');
    });
  });

  describe('JSON logs (nasty table entry)', () => {
    it('a multi-field JSON line with no ambiguity', () => {
      const line = '{"time":"2026-08-28T10:00:00Z","level":"warn","caller":"main.go:10","msg":"cache nearly full","pct":91.2}';
      expect(classifyLogLine(line)).toBe('warn');
    });
  });

  describe('bare text (nasty table entry -- no marker at all)', () => {
    it('falls back to other for ordinary unstructured output', () => {
      expect(classifyLogLine('Starting up, version 1.2.3')).toBe('other');
      expect(classifyLogLine('GET /health 200 1234us')).toBe('other');
      expect(classifyLogLine('')).toBe('other');
    });

    it('does not misfire on a longer word that merely CONTAINS a level word as a substring', () => {
      expect(classifyLogLine('3 warnings found during migration')).toBe('other'); // "warnings" != "warn"/"warning" at a \b boundary
      expect(classifyLogLine('additional information available')).toBe('other'); // "information" != "info" at a \b boundary
      expect(classifyLogLine('erroring out is not the same as erroneous')).toBe('other');
    });
  });
});

describe('SEVERITY_ORDER', () => {
  it('is the filter chips’ own left-to-right order: Error, Warn, Info, Debug, Other', () => {
    expect(SEVERITY_ORDER).toEqual(['error', 'warn', 'info', 'debug', 'other']);
  });
});
