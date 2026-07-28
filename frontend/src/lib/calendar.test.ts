import { afterAll, beforeAll, describe, expect, it, vi } from "vitest";
import {
  bucketByDay,
  dayKey,
  fetchRange,
  startOfWeek,
  stepAnchor,
  visibleDays,
} from "@/lib/calendar";

// Pin the zone so local-day assertions mean something: New York is UTC-4 in
// July, so a UTC evening instant must land on the *previous* local day.
beforeAll(() => {
  vi.stubEnv("TZ", "America/New_York");
});
afterAll(() => {
  vi.unstubAllEnvs();
});

describe("startOfWeek", () => {
  it("returns the Monday of the containing week at local midnight", () => {
    // 2026-07-08 is a Wednesday.
    const monday = startOfWeek(new Date(2026, 6, 8));
    expect(dayKey(monday)).toBe("2026-07-06");
    expect(monday.getHours()).toBe(0);
  });

  it("is a fixed point on a Monday", () => {
    expect(dayKey(startOfWeek(new Date(2026, 6, 6)))).toBe("2026-07-06");
  });
});

describe("visibleDays", () => {
  it("covers the month in full Monday-start weeks", () => {
    // July 2026: the 1st is a Wednesday, the 31st a Friday.
    const days = visibleDays("month", new Date(2026, 6, 15));
    expect(days.length % 7).toBe(0);
    expect(dayKey(days[0])).toBe("2026-06-29");
    expect(dayKey(days[days.length - 1])).toBe("2026-08-02");
  });

  it("shows a single week for the week and agenda views", () => {
    for (const view of ["week", "agenda"] as const) {
      const days = visibleDays(view, new Date(2026, 6, 8));
      expect(days).toHaveLength(7);
      expect(dayKey(days[0])).toBe("2026-07-06");
      expect(dayKey(days[6])).toBe("2026-07-12");
    }
  });
});

describe("stepAnchor", () => {
  it("steps a month at a time in month view, crossing years", () => {
    expect(dayKey(stepAnchor("month", new Date(2026, 11, 15), 1))).toBe(
      "2027-01-01",
    );
    expect(dayKey(stepAnchor("month", new Date(2026, 0, 31), -1))).toBe(
      "2025-12-01",
    );
  });

  it("steps a week at a time otherwise", () => {
    expect(dayKey(stepAnchor("week", new Date(2026, 6, 8), 1))).toBe(
      "2026-07-15",
    );
    expect(dayKey(stepAnchor("agenda", new Date(2026, 6, 8), -1))).toBe(
      "2026-07-01",
    );
  });
});

describe("fetchRange", () => {
  it("spans first day through the day after the last, exclusive", () => {
    const days = [new Date(2026, 6, 6), new Date(2026, 6, 12)];
    const { start, end } = fetchRange(days);
    // Local midnights expressed as instants: UTC-4 in July.
    expect(start).toBe("2026-07-06T04:00:00.000Z");
    expect(end).toBe("2026-07-13T04:00:00.000Z");
  });
});

describe("bucketByDay", () => {
  it("buckets a UTC instant into the viewer's local day", () => {
    // 01:00 UTC is 21:00 the previous evening in New York.
    const buckets = bucketByDay([
      { airs_at: "2026-07-08T01:00:00Z" },
      { airs_at: "2026-07-08T15:00:00Z" },
    ]);
    expect([...buckets.keys()]).toEqual(["2026-07-07", "2026-07-08"]);
  });

  it("treats a bare SQLite timestamp exactly like its ISO UTC form", () => {
    const bare = bucketByDay([{ airs_at: "2026-07-08 01:00:00" }]);
    const iso = bucketByDay([{ airs_at: "2026-07-08T01:00:00Z" }]);
    expect([...bare.keys()]).toEqual([...iso.keys()]);
  });

  it("groups same-day items in order and drops unparseable times", () => {
    const a = { airs_at: "2026-07-08T14:00:00Z", n: 1 };
    const b = { airs_at: "2026-07-08T15:00:00Z", n: 2 };
    const buckets = bucketByDay([a, b, { airs_at: "garbage", n: 3 }]);
    expect(buckets.get("2026-07-08")).toEqual([a, b]);
    expect(buckets.size).toBe(1);
  });
});
