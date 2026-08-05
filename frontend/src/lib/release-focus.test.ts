import { describe, expect, it } from "vitest";
import { filterCovering } from "@/lib/release-focus";

type Row = { id: string; items?: number[] };

const single: Row = { id: "single", items: [7] };
const batch: Row = {
  id: "batch",
  items: [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12],
};
const other: Row = { id: "other", items: [8] };
const unmatched: Row = { id: "unmatched" };
// Not a shape the server emits (omitempty, and a match covers at least one
// item) — the predicate is pinned against it anyway rather than assuming.
const empty: Row = { id: "empty", items: [] };

describe("filterCovering", () => {
  it("keeps a single and a batch covering the focused item", () => {
    expect(filterCovering([single, batch, other], 7)).toEqual([single, batch]);
  });

  it("drops rows with no claim on the focused item", () => {
    expect(filterCovering([other, unmatched, empty], 7)).toEqual([]);
  });
});
