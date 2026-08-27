// stripAnsi removes ANSI SGR/color escape sequences from log text --
// container stdout/stderr routinely carries these (colored log levels,
// progress bars, ...), which would otherwise render as garbage escape
// bytes in LogViewer's plain-text buffer. Deliberately the plan's own
// narrow, literal scope (\x1b\[[0-9;]*m -- SGR/color codes only) rather
// than a general ANSI-sequence parser: it's what container logs
// overwhelmingly actually emit, and a broader parser is out of scope.
const ANSI_SGR = /\x1b\[[0-9;]*m/g;

export function stripAnsi(text: string): string {
  return text.replace(ANSI_SGR, '');
}
