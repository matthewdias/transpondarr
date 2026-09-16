import { describe, expect, it } from "vitest";
import { barSegments } from "@/lib/bar-segments";

describe("barSegments", () => {
  // A long series once shrank a single status below its minimum width, so the
  // track clipped the last segment (#319 review). Asserted against the helper
  // the tab renders rather than through a 1,000-row mount: the claim is
  // arithmetic at large N, and mounting the table cost 1.6s of a 5s timeout.
  it("gives one blocked import in a thousand episodes its minimum width", () => {
    const shown = barSegments([{ key: "stuck", count: 1 }], 1000);

    expect(shown).toHaveLength(1);
    expect(shown[0].width).toBeGreaterThanOrEqual(5);
  });
});
