import { describe, expect, it } from "vitest";
import { filterCovering } from "@/lib/release-focus";

const single = { id: "single", items: [7] };
const batch = { id: "batch", items: [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12] };
const other = { id: "other", items: [8] };
const unmatched = { id: "unmatched" };
const covered = { id: "covered", items: [] };

describe("filterCovering", () => {
  it("keeps a single and a batch covering the focused item", () => {
    expect(filterCovering([single, batch, other], 7)).toEqual([single, batch]);
  });

  // `items` holds only still-wanted numbers, and is absent on an unmatched row —
  // both mean the release offers nothing for the episode being searched.
  it("drops rows with no claim on the focused item", () => {
    expect(filterCovering([other, unmatched, covered], 7)).toEqual([]);
  });
});
