import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { EpisodesTab } from "@/components/detail/episodes-tab";
import type { SeriesDetail, WantedItem } from "@/lib/api";

const item = (over: Partial<WantedItem>): WantedItem => ({
  id: 1,
  number: 1,
  in_library: false,
  monitored: true,
  status: "wanted",
  ...over,
});

const detail = (items: WantedItem[]): SeriesDetail => ({
  id: 7,
  title: "Placeholder Saga",
  format: "TV",
  monitored: true,
  quality_profile_id: 1,
  items,
});

function renderTab(items: WantedItem[]) {
  const onSearchAll = vi.fn();
  const onSearchItem = vi.fn();
  render(
    <EpisodesTab
      detail={detail(items)}
      onSearchAll={onSearchAll}
      onSearchItem={onSearchItem}
      selected={new Set()}
      onToggleSelect={vi.fn()}
      onSelectRange={vi.fn()}
      onSetMonitored={vi.fn()}
    />,
  );
  return { onSearchAll, onSearchItem, user: userEvent.setup() };
}

describe("EpisodesTab search buttons", () => {
  // The row button's intent is "find me this episode", so it must carry the
  // number rather than falling through to the series-wide search.
  it("sends the row's number from a wanted and a deferred episode", async () => {
    const { onSearchAll, onSearchItem, user } = renderTab([
      item({ id: 1, number: 7, status: "wanted" }),
      item({ id: 2, number: 8, status: "deferred" }),
    ]);

    const rows = screen.getAllByRole("button", { name: "Search" });
    await user.click(rows[0]);
    expect(onSearchItem).toHaveBeenCalledWith(7);
    await user.click(rows[1]);
    expect(onSearchItem).toHaveBeenLastCalledWith(8);
    expect(onSearchAll).not.toHaveBeenCalled();
  });

  it("keeps the header button series-wide", async () => {
    const { onSearchAll, onSearchItem, user } = renderTab([
      item({ number: 7, status: "wanted" }),
    ]);

    await user.click(
      screen.getByRole("button", { name: /search all wanted/i }),
    );
    expect(onSearchAll).toHaveBeenCalledTimes(1);
    expect(onSearchItem).not.toHaveBeenCalled();
  });
});

describe("EpisodesTab monitoring", () => {
  // The bulk toolbar is the point: #160's long-runner is narrowed by selecting a
  // range, not by clicking a thousand rows against a 15-minute sweep clock.
  it("sends the selected item ids in one call", async () => {
    const onSetMonitored = vi.fn();
    const user = userEvent.setup();
    render(
      <EpisodesTab
        detail={detail([
          item({ id: 1, number: 1 }),
          item({ id: 2, number: 2 }),
        ])}
        onSearchAll={vi.fn()}
        onSearchItem={vi.fn()}
        selected={new Set([1, 2])}
        onToggleSelect={vi.fn()}
        onSelectRange={vi.fn()}
        onSetMonitored={onSetMonitored}
      />,
    );

    await user.click(screen.getByRole("button", { name: /unmonitor 2/i }));
    expect(onSetMonitored).toHaveBeenCalledWith([1, 2], false);
  });

  it("reports a row checkbox by item id", async () => {
    const onToggleSelect = vi.fn();
    const user = userEvent.setup();
    render(
      <EpisodesTab
        detail={detail([item({ id: 42, number: 3 })])}
        onSearchAll={vi.fn()}
        onSearchItem={vi.fn()}
        selected={new Set()}
        onToggleSelect={onToggleSelect}
        onSelectRange={vi.fn()}
        onSetMonitored={vi.fn()}
      />,
    );

    await user.click(
      screen.getByRole("checkbox", { name: /select episode 3/i }),
    );
    expect(onToggleSelect).toHaveBeenCalledWith(42);
  });

  // The header count and the series-list bar must read the same denominator, or
  // one says 3 / 3 while the other says 3 / 1050.
  it("counts only monitored episodes in the header", () => {
    render(
      <EpisodesTab
        detail={detail([
          item({ id: 1, number: 1, in_library: true, status: "in_library" }),
          item({ id: 2, number: 2 }),
          item({ id: 3, number: 3, monitored: false }),
          item({ id: 4, number: 4, monitored: false }),
        ])}
        onSearchAll={vi.fn()}
        onSearchItem={vi.fn()}
        selected={new Set()}
        onToggleSelect={vi.fn()}
        onSelectRange={vi.fn()}
        onSetMonitored={vi.fn()}
      />,
    );

    expect(screen.getByText("1 / 2")).toBeInTheDocument();
    expect(screen.getByText(/2 not monitored/i)).toBeInTheDocument();
  });
});

describe("EpisodesTab selection range", () => {
  // What makes a 1,000-episode series workable: click the first, shift-click the
  // last. Whole-row click is deliberately not a thing -- it would collide with
  // the in-row Search button.
  it("selects the inclusive range on shift-click", async () => {
    const onSelectRange = vi.fn();
    const onToggleSelect = vi.fn();
    const user = userEvent.setup();
    render(
      <EpisodesTab
        detail={detail([
          item({ id: 10, number: 1 }),
          item({ id: 20, number: 2 }),
          item({ id: 30, number: 3 }),
          item({ id: 40, number: 4 }),
        ])}
        onSearchAll={vi.fn()}
        onSearchItem={vi.fn()}
        selected={new Set()}
        onToggleSelect={onToggleSelect}
        onSelectRange={onSelectRange}
        onSetMonitored={vi.fn()}
      />,
    );

    await user.click(
      screen.getByRole("checkbox", { name: /select episode 2/i }),
    );
    expect(onToggleSelect).toHaveBeenCalledWith(20);

    await user.keyboard("{Shift>}");
    await user.click(
      screen.getByRole("checkbox", { name: /select episode 4/i }),
    );
    await user.keyboard("{/Shift}");
    expect(onSelectRange).toHaveBeenCalledWith([20, 30, 40]);
  });

  // The anchor is the last row clicked, whichever direction the range runs.
  it("ranges backwards from the anchor too", async () => {
    const onSelectRange = vi.fn();
    const user = userEvent.setup();
    render(
      <EpisodesTab
        detail={detail([
          item({ id: 10, number: 1 }),
          item({ id: 20, number: 2 }),
          item({ id: 30, number: 3 }),
        ])}
        onSearchAll={vi.fn()}
        onSearchItem={vi.fn()}
        selected={new Set()}
        onToggleSelect={vi.fn()}
        onSelectRange={onSelectRange}
        onSetMonitored={vi.fn()}
      />,
    );

    await user.click(
      screen.getByRole("checkbox", { name: /select episode 3/i }),
    );
    await user.keyboard("{Shift>}");
    await user.click(
      screen.getByRole("checkbox", { name: /select episode 1/i }),
    );
    await user.keyboard("{/Shift}");
    expect(onSelectRange).toHaveBeenCalledWith([10, 20, 30]);
  });
});

describe("EpisodesTab unmonitored status", () => {
  // "Wanted" is the one status that becomes false when an item is unmonitored;
  // nothing is wanting it. The others stay true and are left alone.
  it("replaces only the wanted badge", () => {
    render(
      <EpisodesTab
        detail={detail([
          item({ id: 1, number: 1, monitored: false, status: "wanted" }),
          item({
            id: 2,
            number: 2,
            monitored: false,
            status: "downloading",
            release_title: "[SynthSubs] Placeholder Saga - 02 [1080p]",
          }),
        ])}
        onSearchAll={vi.fn()}
        onSearchItem={vi.fn()}
        selected={new Set()}
        onToggleSelect={vi.fn()}
        onSelectRange={vi.fn()}
        onSetMonitored={vi.fn()}
      />,
    );

    expect(screen.getByText("Not monitored")).toBeInTheDocument();
    expect(screen.queryByText("Wanted")).not.toBeInTheDocument();
    // An unmonitored episode with a grab in flight really is downloading.
    expect(screen.getByText("Downloading")).toBeInTheDocument();
  });
});
