// Formatting helpers -- the one place raw metric numbers turn into the
// strings every view renders. Every function is total (never throws,
// never returns NaN/undefined text) since a malformed or missing
// metric value must degrade to a harmless placeholder, not break the
// tile/row it's rendered into.

const BYTE_UNITS = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'] as const;

// fmtBytes formats a byte count in binary units (1024-based: KiB, MiB,
// ...), one decimal place at every magnitude including plain bytes
// (uniform decimals keep a column of these visually aligned).
export function fmtBytes(n: number): string {
  if (!Number.isFinite(n)) return '0.0 B';
  const sign = n < 0 ? '-' : '';
  let abs = Math.abs(n);
  let unit = 0;
  while (abs >= 1024 && unit < BYTE_UNITS.length - 1) {
    abs /= 1024;
    unit++;
  }
  return `${sign}${abs.toFixed(1)} ${BYTE_UNITS[unit]}`;
}

const RATE_UNITS = ['B/s', 'KB/s', 'MB/s', 'GB/s', 'TB/s'] as const;

// fmtRate formats a bytes-per-second rate in DECIMAL units (1000-based:
// KB/s, MB/s, ...) -- deliberately not binary, to match docker's own
// convention for throughput figures (see the plan's Global Constraints:
// "show bytes/s units MB/s to match docker convention"). Negative
// input (never a valid rate) clamps to zero rather than printing a
// sign.
export function fmtRate(bps: number): string {
  if (!Number.isFinite(bps) || bps < 0) return '0.0 B/s';
  let val = bps;
  let unit = 0;
  while (val >= 1000 && unit < RATE_UNITS.length - 1) {
    val /= 1000;
    unit++;
  }
  return `${val.toFixed(1)} ${RATE_UNITS[unit]}`;
}

// fmtPct formats a percentage with one decimal place, clamped to the
// 0-100 DISPLAY range -- a metric that's transiently 100.4% (a sampling
// artifact) reads as "100.0%", not something alarmingly out of range.
export function fmtPct(n: number): string {
  if (!Number.isFinite(n)) return '0.0%';
  const clamped = Math.min(100, Math.max(0, n));
  return `${clamped.toFixed(1)}%`;
}

// fmtDuration formats a duration given in seconds as its two biggest
// non-zero units (e.g. "1d 4h", "2h 15m", "45m 10s", "8s") -- compact
// enough for a table cell, precise enough to be useful for uptime.
// Negative/non-finite input (never a valid duration) reads as "0s".
export function fmtDuration(totalSeconds: number): string {
  if (!Number.isFinite(totalSeconds) || totalSeconds < 0) return '0s';
  const s = Math.floor(totalSeconds);
  const days = Math.floor(s / 86400);
  const hours = Math.floor((s % 86400) / 3600);
  const minutes = Math.floor((s % 3600) / 60);
  const seconds = s % 60;

  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${minutes}m`;
  if (minutes > 0) return `${minutes}m ${seconds}s`;
  return `${seconds}s`;
}

// fmtRelTime formats a unix-seconds timestamp as browser-local relative
// time ("just now", "30s ago", "5m ago", "3h ago", "2d ago"). nowMs
// defaults to Date.now() but is an explicit parameter so callers (and
// tests) can pin the reference instant instead of racing the clock.
export function fmtRelTime(ts: number, nowMs: number = Date.now()): string {
  if (!Number.isFinite(ts)) return '';
  const deltaS = Math.floor(nowMs / 1000) - Math.floor(ts);
  if (deltaS < 5) return 'just now';
  if (deltaS < 60) return `${deltaS}s ago`;
  const deltaM = Math.floor(deltaS / 60);
  if (deltaM < 60) return `${deltaM}m ago`;
  const deltaH = Math.floor(deltaM / 60);
  if (deltaH < 24) return `${deltaH}h ago`;
  const deltaD = Math.floor(deltaH / 24);
  return `${deltaD}d ago`;
}
