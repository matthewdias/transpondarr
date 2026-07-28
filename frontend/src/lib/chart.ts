// Season-chart vocabulary and filtering for the discovery page.
import type { SeasonEntry } from "@/lib/api";

const FORMAT_LABELS: Record<string, string> = {
  TV: "TV",
  TV_SHORT: "TV Short",
  MOVIE: "Movie",
  SPECIAL: "Special",
  OVA: "OVA",
  ONA: "ONA",
  MUSIC: "Music",
};

const STATUS_LABELS: Record<string, string> = {
  RELEASING: "Airing",
  NOT_YET_RELEASED: "Upcoming",
  FINISHED: "Finished",
  CANCELLED: "Cancelled",
  HIATUS: "On hiatus",
};

// Unknown provider values pass through verbatim rather than hiding the entry.
export const formatLabel = (f: string) => FORMAT_LABELS[f] ?? f;
export const statusLabel = (s: string) => STATUS_LABELS[s] ?? s;

export interface ChartFilters {
  format: string; // "all" or a provider-native value
  status: string;
  genre: string;
}

export const NO_FILTERS: ChartFilters = {
  format: "all",
  status: "all",
  genre: "all",
};

export function filterEntries(
  entries: SeasonEntry[],
  { format, status, genre }: ChartFilters,
): SeasonEntry[] {
  return entries.filter(
    (e) =>
      (format === "all" || e.format === format) &&
      (status === "all" || e.status === status) &&
      (genre === "all" || e.genres.includes(genre)),
  );
}

// AniList descriptions arrive as an HTML snippet (<br>, <i>, a few entities);
// the cards render plain text, never markup.
export function plainDescription(html: string | undefined): string | null {
  if (!html) return null;
  const text = html
    .replace(/<[^>]*>/g, " ")
    .replace(/&quot;/g, '"')
    .replace(/&#0?39;/g, "'")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&amp;/g, "&")
    .replace(/\s+/g, " ")
    .trim();
  return text || null;
}
