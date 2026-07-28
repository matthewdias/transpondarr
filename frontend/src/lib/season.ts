// Season math for the discovery page, mirroring the backend's AniList
// convention: Jan-Mar winter, Apr-Jun spring, Jul-Sep summer, Oct-Dec fall.

export type SeasonName = "winter" | "spring" | "summer" | "fall";

export interface SeasonRef {
  season: SeasonName;
  year: number;
}

export const SEASONS: SeasonName[] = ["winter", "spring", "summer", "fall"];

export function currentSeason(now = new Date()): SeasonRef {
  const season = SEASONS[Math.floor(now.getMonth() / 3)];
  return { season, year: now.getFullYear() };
}

export function stepSeason(ref: SeasonRef, delta: 1 | -1): SeasonRef {
  const i = SEASONS.indexOf(ref.season) + delta;
  if (i < 0) return { season: "fall", year: ref.year - 1 };
  if (i > 3) return { season: "winter", year: ref.year + 1 };
  return { season: SEASONS[i], year: ref.year };
}

// The API validates season year to a 1940 floor; the picker honors it too.
export const YEAR_FLOOR = 1940;

/** stepSeason bounded to [winter YEAR_FLOOR, fall maxYear]; returns ref at an edge. */
export function stepSeasonClamped(
  ref: SeasonRef,
  delta: 1 | -1,
  maxYear: number,
): SeasonRef {
  const next = stepSeason(ref, delta);
  if (next.year < YEAR_FLOOR || next.year > maxYear) return ref;
  return next;
}

export function seasonLabel(ref: SeasonRef): string {
  const name = ref.season.charAt(0).toUpperCase() + ref.season.slice(1);
  return `${name} ${ref.year}`;
}
