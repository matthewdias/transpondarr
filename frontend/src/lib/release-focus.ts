// `items` carries the still-wanted numbers a release covers, so membership is the
// whole test: it is absent on an unmatched row and empty once nothing is left.
export function filterCovering<T extends { items?: number[] | null }>(
  results: T[],
  n: number,
): T[] {
  return results.filter((r) => r.items?.includes(n) ?? false);
}
