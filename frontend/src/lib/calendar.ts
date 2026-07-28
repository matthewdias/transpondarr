// Date math for the calendar page. Bucketing goes through parseTimestamp so a
// stored UTC instant lands on the viewer's local day — a naive new Date() on a
// zone-less SQLite timestamp would shift episodes across day cells.
import { parseTimestamp } from "@/lib/format";

export type CalendarView = "month" | "week" | "agenda";

/** Local-day identity for one calendar cell, YYYY-MM-DD. */
export function dayKey(d: Date): string {
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${d.getFullYear()}-${m}-${day}`;
}

/** Local calendar-day arithmetic; DST-safe because the Date ctor renormalizes. */
export function addDays(d: Date, n: number): Date {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate() + n);
}

/** Local midnight of the Monday starting d's week. */
export function startOfWeek(d: Date): Date {
  return addDays(d, -((d.getDay() + 6) % 7));
}

/**
 * The days a view shows around `anchor`: full Monday-start weeks covering the
 * anchor's month, or the anchor's single week (week and agenda views).
 */
export function visibleDays(view: CalendarView, anchor: Date): Date[] {
  if (view !== "month") {
    const start = startOfWeek(anchor);
    return Array.from({ length: 7 }, (_, i) => addDays(start, i));
  }
  const last = new Date(anchor.getFullYear(), anchor.getMonth() + 1, 0);
  const days: Date[] = [];
  let d = startOfWeek(new Date(anchor.getFullYear(), anchor.getMonth(), 1));
  while (d <= last || days.length % 7 !== 0) {
    days.push(d);
    d = addDays(d, 1);
  }
  return days;
}

/** Step the anchor one page in `dir`: a month for month view, a week otherwise. */
export function stepAnchor(
  view: CalendarView,
  anchor: Date,
  dir: 1 | -1,
): Date {
  if (view === "month") {
    return new Date(anchor.getFullYear(), anchor.getMonth() + dir, 1);
  }
  return addDays(anchor, dir * 7);
}

/** Query range covering `days`: [first, day-after-last) as RFC 3339 instants. */
export function fetchRange(days: Date[]): { start: string; end: string } {
  return {
    start: days[0].toISOString(),
    end: addDays(days[days.length - 1], 1).toISOString(),
  };
}

/**
 * Group items into local-day cells keyed by dayKey. An unparseable air time is
 * dropped rather than mis-filed; the caller never sees an item without one
 * (the API omits unscheduled rows).
 */
export function bucketByDay<T extends { airs_at: string }>(
  items: T[],
): Map<string, T[]> {
  const buckets = new Map<string, T[]>();
  for (const item of items) {
    const at = parseTimestamp(item.airs_at);
    if (Number.isNaN(at)) continue;
    const key = dayKey(new Date(at));
    const bucket = buckets.get(key);
    if (bucket) bucket.push(item);
    else buckets.set(key, [item]);
  }
  return buckets;
}

/** Local wall-clock air time for a cell entry, e.g. "15:00". */
export function timeLabel(airsAt: string, locale?: string): string {
  const at = parseTimestamp(airsAt);
  if (Number.isNaN(at)) return "";
  return new Date(at).toLocaleTimeString(locale, {
    hour: "2-digit",
    minute: "2-digit",
  });
}
