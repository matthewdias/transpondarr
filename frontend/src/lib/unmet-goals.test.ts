import { describe, expect, it } from "vitest";
import { goalLine, ownGoals, sharedGoals } from "@/lib/unmet-goals";

const g = (label: string, points: number) => ({ label, points });

describe("sharedGoals", () => {
  it("keeps only goals every item carries, in the first item's order", () => {
    const items = [
      { unmet_goals: [g("group TopSubs", 100), g("resolution 1080p", 100)] },
      { unmet_goals: [g("resolution 1080p", 100)] },
    ];
    expect(sharedGoals(items)).toEqual([g("resolution 1080p", 100)]);
  });

  it("treats the same axis at a different depth as a different goal", () => {
    // One item is a rank below the top group, the other two ranks below: both
    // want "group TopSubs" but at different points, so nothing is shared.
    const items = [
      { unmet_goals: [g("group TopSubs", 100)] },
      { unmet_goals: [g("group TopSubs", 200)] },
    ];
    expect(sharedGoals(items)).toEqual([]);
  });

  it("is empty for an empty group and for an item with no goals", () => {
    expect(sharedGoals([])).toEqual([]);
    expect(sharedGoals([{ unmet_goals: [g("dual audio", 100)] }, {}])).toEqual(
      [],
    );
  });

  it("hoists everything when the group has one item", () => {
    const only = { unmet_goals: [g("group TopSubs", 100)] };
    const shared = sharedGoals([only]);
    expect(shared).toEqual([g("group TopSubs", 100)]);
    expect(ownGoals(only, shared)).toEqual([]);
  });
});

describe("ownGoals", () => {
  it("subtracts exactly the shared set", () => {
    const item = {
      unmet_goals: [g("group TopSubs", 100), g("dual audio", 100)],
    };
    expect(ownGoals(item, [g("group TopSubs", 100)])).toEqual([
      g("dual audio", 100),
    ]);
  });
});

describe("goalLine", () => {
  it("renders label and points the way the row does", () => {
    expect(
      goalLine([g("group TopSubs", 100), g("resolution 1080p", 100)]),
    ).toBe("group TopSubs (+100) · resolution 1080p (+100)");
  });
});
