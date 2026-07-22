/** Human-readable byte size, e.g. 1.4 GB. */
export function formatBytes(bytes: number): string {
  if (!bytes || bytes < 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  const digits = value >= 100 || unit === 0 ? 0 : 1
  return `${value.toFixed(digits)} ${units[unit]}`
}

/** Compact relative time from an ISO/SQLite timestamp, e.g. "2h ago". */
export function timeAgo(input: string): string {
  // SQLite datetime('now') is UTC without a zone suffix; treat it as UTC.
  const iso = input.includes('T') ? input : input.replace(' ', 'T') + 'Z'
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return input
  const secs = Math.max(0, (Date.now() - then) / 1000)
  if (secs < 60) return 'just now'
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `${mins}m ago`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  if (days < 30) return `${days}d ago`
  const months = Math.floor(days / 30)
  if (months < 12) return `${months}mo ago`
  return `${Math.floor(months / 12)}y ago`
}

/** Two-digit zero-padded episode number for the mono column. */
export function pad2(n: number): string {
  return String(n).padStart(2, '0')
}
