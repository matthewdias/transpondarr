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
export function parseTimestamp(input: string): number {
  // SQLite datetime('now') is UTC without a zone suffix; treat it as UTC.
  const iso = input.includes("T") ? input : input.replace(" ", "T") + "Z";
  return new Date(iso).getTime();
}

/** Relative countdown while within a week of airing; null outside that window. */
function countdown(secs: number): string | null {
  if (secs <= 0 || secs >= 7 * 86400) return null;
  if (secs < 60) return "any moment";
  if (secs < 3600) return `in ${Math.floor(secs / 60)}m`;
  if (secs < 86400) return `in ${Math.floor(secs / 3600)}h`;
  return `in ${Math.floor(secs / 86400)}d`;
}

// locale defaults to the viewer's; tests pin it so assertions are not at the
// mercy of the runner's ICU default.
function absoluteDate(at: number, locale?: string): string {
  return new Date(at).toLocaleDateString(locale, {
    day: "numeric",
    month: "short",
    year: "numeric",
  });
}

/** A future instant: a countdown while within a week, an absolute date beyond. */
export function countdownOrDate(
  input: string | undefined,
  locale?: string,
): string {
  if (!input) return "—";
  const at = parseTimestamp(input);
  if (Number.isNaN(at)) return "—";
  const secs = (at - Date.now()) / 1000;
  return countdown(secs) ?? absoluteDate(at, locale);
}

/**
 * Broadcast time for an episode row. AniList publishes no schedule for many
 * older titles, so an absent date renders as a placeholder rather than an error.
 */
export function airDate(input: string | undefined, locale?: string): string {
  return countdownOrDate(input, locale);
}

/**
 * A film's date where a column has room for the date alone. Never counted down
 * and never clocked: the stored instant may be a date-only release held at noon
 * UTC to name a day (#224), so anything finer would be invented precision.
 */
export function premiereDate(
  input: string | undefined,
  locale?: string,
): string {
  if (!input) return "—";
  const at = parseTimestamp(input);
  if (Number.isNaN(at)) return "—";
  return absoluteDate(at, locale);
}

/**
 * Next-episode line for a discovery card. The season cache can lag a broadcast
 * by hours, so a non-positive countdown clamps to "aired" instead of counting
 * negative time.
 */
export function nextEpisodeLabel(
  number: number | undefined,
  airsAt: string | undefined,
  locale?: string,
): string | null {
  if (!airsAt) return null;
  const at = parseTimestamp(airsAt);
  if (Number.isNaN(at)) return null;
  const ep = number ? `Ep ${number}` : "Next ep";
  const secs = (at - Date.now()) / 1000;
  if (secs <= 0) return `${ep} aired`;
  const rel = countdown(secs);
  return rel ? `${ep} ${rel}` : `${ep} on ${absoluteDate(at, locale)}`;
}

/**
 * A film's release line, tensed on the date and never counted down: the stored
 * instant may be a date-only release held at noon UTC to name a day, so an
 * "in 4h" would state precision the provider never published.
 */
export function premiereLabel(
  airsAt: string | undefined,
  locale?: string,
): string | null {
  if (!airsAt) return null;
  const at = parseTimestamp(airsAt);
  if (Number.isNaN(at)) return null;
  const verb = at > Date.now() ? "Premieres" : "Released";
  return `${verb} ${absoluteDate(at, locale)}`;
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

/**
 * "3 releases" / "1 series". Takes an explicit plural, because the words this
 * UI counts include ones that do not take an s.
 */
export function plural(n: number, one: string, many = `${one}s`): string {
  return `${n} ${n === 1 ? one : many}`;
}

/** Two-digit zero-padded episode number for the mono column. */
export function pad2(n: number): string {
  return String(n).padStart(2, "0");
}
