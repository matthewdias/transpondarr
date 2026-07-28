import { describe, expect, it } from "vitest";
import type { SeasonEntry } from "@/lib/api";
import {
  filterEntries,
  plainDescription,
  formatLabel,
  NO_FILTERS,
  statusLabel,
} from "@/lib/chart";

const entry = (over: Partial<SeasonEntry>): SeasonEntry => ({
  anilist_id: 1,
  episodes: 12,
  genres: [],
  average_score: 0,
  tracked: false,
  ...over,
});

describe("filterEntries", () => {
  const chart = [
    entry({
      anilist_id: 1,
      format: "TV",
      status: "RELEASING",
      genres: ["Action", "Comedy"],
    }),
    entry({
      anilist_id: 2,
      format: "MOVIE",
      status: "FINISHED",
      genres: ["Drama"],
    }),
    entry({
      anilist_id: 3,
      format: "TV",
      status: "NOT_YET_RELEASED",
      genres: ["Comedy"],
    }),
  ];

  it("passes everything through unfiltered", () => {
    expect(filterEntries(chart, NO_FILTERS)).toHaveLength(3);
  });

  it("filters each dimension independently", () => {
    expect(
      filterEntries(chart, { ...NO_FILTERS, format: "TV" }).map(
        (e) => e.anilist_id,
      ),
    ).toEqual([1, 3]);
    expect(
      filterEntries(chart, { ...NO_FILTERS, status: "FINISHED" }).map(
        (e) => e.anilist_id,
      ),
    ).toEqual([2]);
    expect(
      filterEntries(chart, { ...NO_FILTERS, genre: "Comedy" }).map(
        (e) => e.anilist_id,
      ),
    ).toEqual([1, 3]);
  });

  it("intersects dimensions", () => {
    expect(
      filterEntries(chart, {
        format: "TV",
        status: "RELEASING",
        genre: "Comedy",
      }).map((e) => e.anilist_id),
    ).toEqual([1]);
  });
});

describe("labels", () => {
  it("prettifies known provider values", () => {
    expect(formatLabel("TV_SHORT")).toBe("TV Short");
    expect(statusLabel("RELEASING")).toBe("Airing");
  });

  it("passes unknown values through verbatim", () => {
    expect(formatLabel("HOLOGRAM")).toBe("HOLOGRAM");
    expect(statusLabel("PAUSED")).toBe("PAUSED");
  });
});

describe("plainDescription", () => {
  it("returns null when absent or empty after stripping", () => {
    expect(plainDescription(undefined)).toBeNull();
    expect(plainDescription("")).toBeNull();
    expect(plainDescription("<i></i>")).toBeNull();
  });

  it("strips AniList's HTML markup and collapses whitespace", () => {
    expect(
      plainDescription("A hero rises.<br><br><i>Adapted from the manga.</i>"),
    ).toBe("A hero rises. Adapted from the manga.");
  });

  it("decodes the common entities AniList emits", () => {
    expect(
      plainDescription("&quot;Go,&quot; she said &amp; left. It&#039;s over."),
    ).toBe('"Go," she said & left. It\'s over.');
  });
});
