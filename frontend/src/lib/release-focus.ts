// `items` is the numbers a release covers for this series, so membership is the
// whole test; an unmatched release carries none.
export function filterCovering<T extends { items?: number[] | null }>(
  results: T[],
  n: number,
): T[] {
  return results.filter((r) => r.items?.includes(n) ?? false);
}
