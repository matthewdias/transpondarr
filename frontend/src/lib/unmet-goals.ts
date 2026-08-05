// Hoisting for the Cutoff Unmet group header: the goals every item in a group
// shares are said once on the header, and a row keeps only what is its own.
// Identity is label AND points -- the same axis at a different depth (one item
// two group-ranks down, another one) is not the same goal.

type Goal = { label: string; points: number };

function sameGoal(a: Goal, b: Goal): boolean {
  return a.label === b.label && a.points === b.points;
}

/** The goals common to every item, in the first item's order. */
export function sharedGoals(items: { unmet_goals?: Goal[] }[]): Goal[] {
  if (items.length === 0) return [];
  return (items[0].unmet_goals ?? []).filter((g) =>
    items.every((it) => (it.unmet_goals ?? []).some((o) => sameGoal(o, g))),
  );
}

/** An item's goals minus the shared ones the header already states. */
export function ownGoals(
  item: { unmet_goals?: Goal[] },
  shared: Goal[],
): Goal[] {
  return (item.unmet_goals ?? []).filter(
    (g) => !shared.some((s) => sameGoal(s, g)),
  );
}

/** "group TopSubs (+100) · resolution 1080p (+100)" */
export function goalLine(goals: Goal[]): string {
  return goals.map((g) => `${g.label} (+${g.points})`).join(" · ");
}
