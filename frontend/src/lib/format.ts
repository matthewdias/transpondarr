/** Human-readable byte size, e.g. 1.4 GB. */
export function formatBytes(bytes: number): string {
  if (!bytes || bytes < 0) return "—";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  const digits = value >= 100 || unit === 0 ? 0 : 1;
  return `${value.toFixed(digits)} ${units[unit]}`;
}

/** Epoch millis from an ISO or bare SQLite timestamp; NaN when unparseable. */
function parseTimestamp(input: string): number {
  // SQLite datetime('now') is UTC without a zone suffix; treat it as UTC.
  const iso = input.includes("T") ? input : input.replace(" ", "T") + "Z";
  return new Date(iso).getTime();
}

/**
 * Broadcast time for an episode row: a countdown while it is within a week of
 * airing, an absolute date otherwise. AniList publishes no schedule for many
 * older titles, so an absent date renders as a placeholder rather than an error.
 */
export function airDate(input: string | undefined, locale?: string): string {
  if (!input) return "—";
  const at = parseTimestamp(input);
  if (Number.isNaN(at)) return "—";

  const secs = (at - Date.now()) / 1000;
  if (secs > 0 && secs < 7 * 86400) {
    if (secs < 60) return "any moment";
    if (secs < 3600) return `in ${Math.floor(secs / 60)}m`;
    if (secs < 86400) return `in ${Math.floor(secs / 3600)}h`;
    return `in ${Math.floor(secs / 86400)}d`;
  }
  // locale defaults to the viewer's; tests pin it so the assertion is not at the
  // mercy of the runner's ICU default.
  return new Date(at).toLocaleDateString(locale, {
    day: "numeric",
    month: "short",
    year: "numeric",
  });
}

/** Compact relative time from an ISO/SQLite timestamp, e.g. "2h ago". */
export function timeAgo(input: string): string {
  const then = parseTimestamp(input);
  if (Number.isNaN(then)) return input;
  const secs = Math.max(0, (Date.now() - then) / 1000);
  if (secs < 60) return "just now";
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days}d ago`;
  const months = Math.floor(days / 30);
  if (months < 12) return `${months}mo ago`;
  return `${Math.floor(months / 12)}y ago`;
}

/** Two-digit zero-padded episode number for the mono column. */
export function pad2(n: number): string {
  return String(n).padStart(2, "0");
}
