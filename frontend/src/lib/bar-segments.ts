// The narrowest track is 120px, so 5% keeps every segment at least 6px wide.
const minSegment = 5;

// The floors overflow the track by under 15%, which leaves the largest segment far
// above its own floor, so it absorbs the overflow alone (in library, on a long series).
export function barSegments<T extends { count: number }>(
  segs: T[],
  total: number,
) {
  const pct = (n: number) => (total > 0 ? (n / total) * 100 : 0);
  const shown = segs
    .filter((s) => s.count > 0)
    .map((s) => ({ ...s, width: Math.max(pct(s.count), minSegment) }));
  const excess = shown.reduce((sum, s) => sum + s.width, 0) - 100;
  if (excess > 0)
    shown.reduce((a, b) => (b.width > a.width ? b : a)).width -= excess;
  return shown;
}
