// Pure log-line severity classification for LogViewer's filter chips
// (All/Error/Warn/Info/Debug/Other). Deliberately a single tolerant
// word-boundary match over every known level word rather than a
// separate regex per surface form ([ERROR], ERROR:, level=warn,
// "level":"error" JSON, FATAL/PANIC/NOTICE/TRACE): every one of those
// forms already CONTAINS the bare level word at a word boundary
// (brackets/colons/quotes/= are all non-word characters), so one
// `\bword\b` scan naturally recognizes all of them -- and stays
// unfooled by a longer unrelated word merely containing one as a
// substring ("warnings", "information") since \b requires the match to
// end there too. glog's single-letter-plus-date prefix (E0828/W0828/...)
// is the one real exception (no whole level WORD appears at all), so it
// gets its own small pattern.

export type LogSeverity = 'error' | 'warn' | 'info' | 'debug' | 'other';

// SEVERITY_ORDER is the filter chips' own left-to-right order (All is a
// separate pseudo-value the UI adds itself, not a LogSeverity).
export const SEVERITY_ORDER: LogSeverity[] = ['error', 'warn', 'info', 'debug', 'other'];

// LEVEL_WORD_SEVERITY maps every recognized level word (lowercased) to
// its canonical bucket -- fatal/panic/critical/crit all read as error,
// notice as info, trace as debug, matching common logging frameworks'
// own extended vocabularies beyond the plain four.
const LEVEL_WORD_SEVERITY: Record<string, LogSeverity> = {
  error: 'error',
  err: 'error',
  fatal: 'error',
  panic: 'error',
  critical: 'error',
  crit: 'error',
  warn: 'warn',
  warning: 'warn',
  info: 'info',
  notice: 'info',
  debug: 'debug',
  trace: 'debug',
};

const LEVEL_WORD_RE = new RegExp(`\\b(${Object.keys(LEVEL_WORD_SEVERITY).join('|')})\\b`, 'i');

// GLOG_RE matches glog's own line-start convention (a single E/W/I/F
// letter immediately followed by a 4-digit mmdd, e.g. "E0828 12:34:56.789012
// 123 file.go:12] message") -- glog has no distinct debug level, so D is
// deliberately not included here (a bare "debug"/"trace" elsewhere in the
// line already matches via LEVEL_WORD_RE regardless).
const GLOG_RE = /^([EWIF])\d{4}\b/i;
const GLOG_LETTER_SEVERITY: Record<string, LogSeverity> = { e: 'error', w: 'warn', i: 'info', f: 'error' };

// ANSI_SGR mirrors lib/ansi.ts's own stripAnsi -- LogViewer already
// strips these before a line ever reaches this classifier, but stripping
// again here (cheap; a no-op on already-clean text) makes classifyLogLine
// correct on its own terms for any other caller, and specifically keeps
// GLOG_RE's line-start anchor working when a raw line opens with a color
// code before its level letter.
const ANSI_SGR = /\x1b\[[0-9;]*m/g;

// classifyLogLine picks the EARLIEST severity token in the line --
// whichever of the word match or the glog match starts at the lower
// index wins (a genuine tie can't actually happen: a glog letter is
// never itself a whole level word, so the two patterns can't match the
// same starting position). A line with neither present is 'other', not
// an error.
export function classifyLogLine(line: string): LogSeverity {
  const clean = line.replace(ANSI_SGR, '');
  const wordMatch = LEVEL_WORD_RE.exec(clean);
  const glogMatch = GLOG_RE.exec(clean);
  const wordIndex = wordMatch ? wordMatch.index : Infinity;
  const glogIndex = glogMatch ? glogMatch.index : Infinity;
  if (wordIndex === Infinity && glogIndex === Infinity) return 'other';
  if (glogIndex <= wordIndex) return GLOG_LETTER_SEVERITY[glogMatch![1].toLowerCase()];
  return LEVEL_WORD_SEVERITY[wordMatch![1].toLowerCase()];
}
