import { describe, expect, it } from "vitest";
import {
  currentSeason,
  seasonLabel,
  stepSeason,
  stepSeasonClamped,
  YEAR_FLOOR,
} from "@/lib/season";

describe("currentSeason", () => {
  it("maps months onto the AniList quarters", () => {
    expect(currentSeason(new Date("2026-01-15T12:00:00Z"))).toEqual({
      season: "winter",
      year: 2026,
    });
    expect(currentSeason(new Date("2026-04-01T12:00:00Z"))).toEqual({
      season: "spring",
      year: 2026,
    });
    expect(currentSeason(new Date("2026-07-28T12:00:00Z"))).toEqual({
      season: "summer",
      year: 2026,
    });
    expect(currentSeason(new Date("2026-12-31T12:00:00Z"))).toEqual({
      season: "fall",
      year: 2026,
    });
  });
});

describe("stepSeason", () => {
  it("steps forward and back within a year", () => {
    expect(stepSeason({ season: "spring", year: 2026 }, 1)).toEqual({
      season: "summer",
      year: 2026,
    });
    expect(stepSeason({ season: "spring", year: 2026 }, -1)).toEqual({
      season: "winter",
      year: 2026,
    });
  });

  it("carries across year boundaries", () => {
    expect(stepSeason({ season: "fall", year: 2026 }, 1)).toEqual({
      season: "winter",
      year: 2027,
    });
    expect(stepSeason({ season: "winter", year: 2026 }, -1)).toEqual({
      season: "fall",
      year: 2025,
    });
  });
});

describe("seasonLabel", () => {
  it("renders a title-cased season with its year", () => {
    expect(seasonLabel({ season: "summer", year: 2026 })).toBe("Summer 2026");
  });
});

describe("stepSeasonClamped", () => {
  it("steps normally inside the range", () => {
    expect(
      stepSeasonClamped({ season: "spring", year: 2026 }, 1, 2027),
    ).toEqual({ season: "summer", year: 2026 });
  });

  it("refuses to step below winter of the floor year", () => {
    const floor = { season: "winter" as const, year: YEAR_FLOOR };
    expect(stepSeasonClamped(floor, -1, 2027)).toBe(floor);
  });

  it("refuses to step past fall of the ceiling year", () => {
    const ceiling = { season: "fall" as const, year: 2027 };
    expect(stepSeasonClamped(ceiling, 1, 2027)).toBe(ceiling);
  });
});
