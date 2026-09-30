import { describe, expect, it } from "vitest";
import {
  airDate,
  formatBytes,
  nextEpisodeLabel,
  pad2,
  premiereLabel,
  plural,
  timeAgo,
} from "@/lib/format";

// `now` is passed explicitly and differs from the runner's clock, so a formatter
// that read Date.now() instead would fail here.
const at = (iso: string) => Date.parse(iso);

describe("formatBytes", () => {
  it("renders a placeholder for zero or negative sizes", () => {
    expect(formatBytes(0)).toBe("—");
    expect(formatBytes(-1)).toBe("—");
  });

  it("keeps sub-KB values in whole bytes", () => {
    expect(formatBytes(1)).toBe("1 B");
    expect(formatBytes(1023)).toBe("1023 B");
  });

  it("scales through the units with one decimal", () => {
    expect(formatBytes(1024)).toBe("1.0 KB");
    expect(formatBytes(1536)).toBe("1.5 KB");
    expect(formatBytes(1.4 * 1024 ** 3)).toBe("1.4 GB");
  });

  it("drops the decimal once the value reaches three digits", () => {
    expect(formatBytes(100 * 1024 ** 2)).toBe("100 MB");
  });

  it("caps at TB instead of using a larger unit", () => {
    expect(formatBytes(1024 ** 5)).toBe("1024 TB");
  });
});

describe("timeAgo", () => {
  it("treats a bare SQLite timestamp as UTC", () => {
    const now = at("2026-07-23T12:00:00Z");
    expect(timeAgo("2026-07-23 10:00:00", now)).toBe("2h ago");
  });

  it("steps through the coarser units", () => {
    const now = at("2026-07-23T12:00:00Z");
    expect(timeAgo("2026-07-23T11:59:30Z", now)).toBe("just now");
    expect(timeAgo("2026-07-23T11:15:00Z", now)).toBe("45m ago");
    expect(timeAgo("2026-07-20T12:00:00Z", now)).toBe("3d ago");
    expect(timeAgo("2026-05-23T12:00:00Z", now)).toBe("2mo ago");
    expect(timeAgo("2024-07-23T12:00:00Z", now)).toBe("2y ago");
  });

  it("clamps future timestamps to just now", () => {
    const now = at("2026-07-23T12:00:00Z");
    expect(timeAgo("2026-07-23T13:00:00Z", now)).toBe("just now");
  });

  it("returns unparseable input unchanged", () => {
    expect(timeAgo("not a timestamp", at("2026-07-23T12:00:00Z"))).toBe(
      "not a timestamp",
    );
  });
});

describe("pad2", () => {
  it("zero-pads single digits", () => {
    expect(pad2(0)).toBe("00");
    expect(pad2(5)).toBe("05");
  });

  it("leaves two or more digits alone", () => {
    expect(pad2(12)).toBe("12");
    expect(pad2(112)).toBe("112");
  });
});

describe("airDate", () => {
  it("renders an absolute date for an episode that has aired", () => {
    const now = at("2026-07-23T12:00:00Z");
    expect(airDate("2026-01-04T15:30:00Z", now, "en-GB")).toBe("4 Jan 2026");
  });

  it("counts down to an upcoming episode instead of dating it", () => {
    const now = at("2026-07-23T12:00:00Z");
    expect(airDate("2026-07-23T13:30:00Z", now)).toBe("in 1h");
    expect(airDate("2026-07-26T12:00:00Z", now)).toBe("in 3d");
    expect(airDate("2026-07-23T12:00:30Z", now)).toBe("any moment");
  });

  it("stops counting down past a week out", () => {
    const now = at("2026-07-23T12:00:00Z");
    expect(airDate("2026-09-01T12:00:00Z", now, "en-GB")).toBe("1 Sept 2026");
  });

  // AniList publishes no schedule for many older titles, so an absent date is a
  // normal row rather than an error.
  it("renders a placeholder for a missing or unparseable date", () => {
    const now = at("2026-07-23T12:00:00Z");
    expect(airDate(undefined, now)).toBe("—");
    expect(airDate("", now)).toBe("—");
    expect(airDate("not a timestamp", now)).toBe("—");
  });
});

describe("nextEpisodeLabel", () => {
  it("returns null without a scheduled time", () => {
    const now = at("2026-07-23T12:00:00Z");
    expect(nextEpisodeLabel(6, undefined, now)).toBeNull();
    expect(nextEpisodeLabel(6, "garbage", now)).toBeNull();
  });

  it("counts down to an upcoming episode", () => {
    const now = at("2026-07-23T12:00:00Z");
    expect(nextEpisodeLabel(6, "2026-07-26T12:30:00Z", now)).toBe("Ep 6 in 3d");
    expect(nextEpisodeLabel(2, "2026-07-23T13:30:00Z", now)).toBe("Ep 2 in 1h");
  });

  it("clamps a stale timestamp to aired instead of a negative countdown", () => {
    // The season cache can lag ~6h behind a broadcast.
    const now = at("2026-07-23T12:00:00Z");
    expect(nextEpisodeLabel(6, "2026-07-23T09:00:00Z", now)).toBe("Ep 6 aired");
  });

  it("falls back to an absolute date beyond a week out", () => {
    const now = at("2026-07-01T12:00:00Z");
    expect(nextEpisodeLabel(13, "2026-08-20T12:00:00Z", now, "en-US")).toBe(
      "Ep 13 on Aug 20, 2026",
    );
  });

  it("handles a missing episode number", () => {
    const now = at("2026-07-23T12:00:00Z");
    expect(nextEpisodeLabel(undefined, "2026-07-26T12:30:00Z", now)).toBe(
      "Next ep in 3d",
    );
  });
});

describe("premiereLabel", () => {
  it("returns null without a date", () => {
    const now = at("2026-03-15T08:00:00Z");
    expect(premiereLabel(undefined, now)).toBeNull();
    expect(premiereLabel("garbage", now)).toBeNull();
  });

  // A film's stored date may be a date-only release set to noon UTC, so a
  // countdown would state precision that was never published.
  it("never counts down, however close the date", () => {
    const now = at("2026-03-15T08:00:00Z");
    expect(premiereLabel("2026-03-15T12:00:00Z", now, "en-US")).toBe(
      "Premieres Mar 15, 2026",
    );
    expect(premiereLabel("2026-03-18T12:00:00Z", now, "en-US")).toBe(
      "Premieres Mar 18, 2026",
    );
  });

  // "Released" beside a future date is wrong on its own terms.
  it("tenses on the date rather than calling a future release past", () => {
    const now = at("2026-06-01T12:00:00Z");
    expect(premiereLabel("2026-03-15T12:00:00Z", now, "en-US")).toBe(
      "Released Mar 15, 2026",
    );
  });
});

describe("plural", () => {
  it("counts and pluralizes in one go", () => {
    expect(plural(0, "release")).toBe("0 releases");
    expect(plural(1, "release")).toBe("1 release");
    expect(plural(2, "release")).toBe("2 releases");
  });

  // "series" is its own plural, and the count line read "2 seriess" before this
  // helper took an explicit plural.
  it("takes an explicit plural for words that do not take an s", () => {
    expect(plural(1, "series", "series")).toBe("1 series");
    expect(plural(3, "series", "series")).toBe("3 series");
  });
});
