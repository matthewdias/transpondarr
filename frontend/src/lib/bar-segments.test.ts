import { describe, expect, it } from "vitest";
import { barSegments } from "@/lib/bar-segments";

// Asserted against the helper the episodes tab imports, not through the tab: the
// three need 191-1,000 item rows each, and together cost 2.7s to render (#319).
const widthsFor = (
  counts: Partial<
    Record<"inLibrary" | "downloading" | "deferred" | "stuck", number>
  >,
  total: number,
) =>
  barSegments(
    [
      { key: "in_library", count: counts.inLibrary ?? 0 },
      { key: "downloading", count: counts.downloading ?? 0 },
      { key: "deferred", count: counts.deferred ?? 0 },
      { key: "stuck", count: counts.stuck ?? 0 },
    ],
    total,
  ).map((s) => s.width);

describe("barSegments", () => {
  it("gives one blocked import in a thousand episodes its minimum width", () => {
    const widths = widthsFor({ stuck: 1 }, 1000);

    expect(widths).toHaveLength(1);
    expect(widths[0]).toBeGreaterThanOrEqual(5);
  });

  // A long series once shrank a single status below its minimum width, so the
  // track clipped the last segment (#319 review).
  it("keeps every status visible inside the track on a long series", () => {
    const widths = widthsFor(
      { inLibrary: 197, downloading: 1, deferred: 1, stuck: 1 },
      200,
    );

    expect(widths).toHaveLength(4);
    const [library, ...rest] = widths;
    for (const w of rest) expect(w).toBeGreaterThanOrEqual(5);
    expect(library).toBeGreaterThan(Math.max(...rest));
    expect(widths.reduce((a, b) => a + b, 0)).toBeLessThanOrEqual(100);
  });

  // Only the in-library segment gives up width, so downloading keeps its true share.
  it("takes the overflow from the in-library segment alone", () => {
    const [library, downloading, stuck] = widthsFor(
      { inLibrary: 120, downloading: 70, stuck: 1 },
      191,
    );

    expect(downloading).toBeCloseTo((70 / 191) * 100, 5);
    expect(stuck).toBeGreaterThanOrEqual(5);
    expect(library + downloading + stuck).toBeCloseTo(100, 5);
  });
});
