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

export function seasonLabel(ref: SeasonRef): string {
  const name = ref.season.charAt(0).toUpperCase() + ref.season.slice(1);
  return `${name} ${ref.year}`;
}
