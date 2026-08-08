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
      onSetSelection={vi.fn()}
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

  // A held episode is a legitimate grab candidate since #97, and Cutoff Unmet
  // already links to this same episode-filtered view.
  it("offers a held episode the same row search", async () => {
    const { onSearchItem, user } = renderTab([
      item({ id: 1, number: 4, in_library: true, status: "in_library" }),
    ]);

    await user.click(screen.getByRole("button", { name: "Search" }));
    expect(onSearchItem).toHaveBeenCalledWith(4);
  });

  // A live grab is no reason to hide it: the Releases tab grabs at any status,
  // so a condition here would gate nothing -- and a stuck import, which retries
  // the same release forever, is exactly when another release is wanted.
  it("offers it while a grab is in flight or its import is stuck", async () => {
    const { onSearchItem, user } = renderTab([
      item({ id: 1, number: 5, status: "downloading" }),
      item({ id: 2, number: 6, status: "stuck" }),
    ]);

    const rows = screen.getAllByRole("button", { name: "Search" });
    expect(rows).toHaveLength(2);
    await user.click(rows[1]);
    expect(onSearchItem).toHaveBeenCalledWith(6);
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

const past = new Date(Date.now() - 86_400_000).toISOString();
const future = new Date(Date.now() + 86_400_000).toISOString();

// The summary strip is what these assert; the callbacks are noise here.
function renderStrip(items: WantedItem[]) {
  render(
    <EpisodesTab
      detail={detail(items)}
      onSearchAll={vi.fn()}
      onSearchItem={vi.fn()}
      selected={new Set()}
      onToggleSelect={vi.fn()}
      onSelectRange={vi.fn()}
      onSetSelection={vi.fn()}
      onSetMonitored={vi.fn()}
    />,
  );
}

describe("EpisodesTab monitoring", () => {
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
        onSetSelection={vi.fn()}
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
        onSetSelection={vi.fn()}
        onSetMonitored={vi.fn()}
      />,
    );

    await user.click(
      screen.getByRole("checkbox", { name: /select episode 3/i }),
    );
    expect(onToggleSelect).toHaveBeenCalledWith(42);
  });

  // The header count and the series-list bar must read the same denominator, or
  // one says 3 / 3 while the other says 3 / 1050. That means the server's exact
  // definition of tracked: monitored AND already broadcast.
  it("counts only monitored, aired episodes in the header", () => {
    renderStrip([
      item({ id: 1, number: 1, in_library: true, status: "in_library" }),
      item({ id: 2, number: 2 }),
      item({ id: 3, number: 3, monitored: false }),
      item({ id: 4, number: 4, monitored: false }),
    ]);

    expect(screen.getByText("1 / 2")).toBeInTheDocument();
    expect(screen.getByText(/2 not monitored/i)).toBeInTheDocument();
  });

  // A null air date reads as aired -- AniList's coverage is thin by design --
  // so the denominator is neither 0 nor the full monitored count here.
  it("keeps an unaired monitored episode out of the denominator", () => {
    renderStrip([
      item({
        id: 1,
        number: 1,
        in_library: true,
        status: "in_library",
        airs_at: past,
      }),
      item({ id: 2, number: 2, airs_at: past }),
      item({ id: 3, number: 3 }), // no air date: searchable, so tracked
      item({ id: 4, number: 4, airs_at: future }),
      item({ id: 5, number: 5, airs_at: future }),
    ]);

    expect(screen.getByText("1 / 3")).toBeInTheDocument();
    expect(screen.getByText(/2 not yet aired/i)).toBeInTheDocument();
  });

  // The three categories partition every item, so a reader can add them up.
  it("accounts for every item across tracked, unaired and unmonitored", () => {
    renderStrip([
      item({
        id: 1,
        number: 1,
        in_library: true,
        status: "in_library",
        airs_at: past,
      }),
      item({ id: 2, number: 2, airs_at: past }),
      item({ id: 3, number: 3, airs_at: future }),
      item({ id: 4, number: 4, monitored: false }),
      item({ id: 5, number: 5, monitored: false }),
      item({ id: 6, number: 6, monitored: false }),
    ]);

    // 2 tracked + 1 unaired + 3 unmonitored = 6
    expect(screen.getByText("1 / 2")).toBeInTheDocument();
    expect(screen.getByText(/1 not yet aired/i)).toBeInTheDocument();
    expect(screen.getByText(/3 not monitored/i)).toBeInTheDocument();
    expect(screen.getByText(/6 total/i)).toBeInTheDocument();
  });

  it("words the zero state and keeps the total beside it", () => {
    renderStrip([
      item({ id: 1, number: 1, airs_at: future }),
      item({ id: 2, number: 2, airs_at: future }),
    ]);

    expect(screen.getByText("Nothing aired yet")).toBeInTheDocument();
    expect(screen.queryByText("0 / 0")).not.toBeInTheDocument();
    expect(screen.getByText(/2 not yet aired/i)).toBeInTheDocument();
    expect(screen.getByText(/2 total/i)).toBeInTheDocument();
  });

  // The same empty denominator, the other cause: every episode has aired and
  // every one was switched off, where "nothing aired yet" is simply false.
  it("names monitoring when that is what emptied the denominator", () => {
    renderStrip([
      item({ id: 1, number: 1, monitored: false }),
      item({ id: 2, number: 2, monitored: false }),
    ]);

    expect(screen.getByText("Nothing monitored")).toBeInTheDocument();
    expect(screen.queryByText("Nothing aired yet")).not.toBeInTheDocument();
    expect(screen.getByText(/2 not monitored/i)).toBeInTheDocument();
  });

  // The raw count is redundant once it is already the denominator.
  it("drops the total when everything is tracked", () => {
    renderStrip([
      item({ id: 1, number: 1, in_library: true, status: "in_library" }),
      item({ id: 2, number: 2 }),
    ]);

    expect(screen.getByText("1 / 2")).toBeInTheDocument();
    expect(screen.queryByText(/total/i)).not.toBeInTheDocument();
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
        onSetSelection={vi.fn()}
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
        onSetSelection={vi.fn()}
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

  // The anchor is an item id, not a row position: a refetch between the two
  // clicks renumbers the rows, and an index would then span the wrong episodes.
  it("re-resolves the anchor against the rows it is given", async () => {
    const onSelectRange = vi.fn();
    const user = userEvent.setup();
    const props = {
      onSearchAll: vi.fn(),
      onSearchItem: vi.fn(),
      selected: new Set<number>(),
      onToggleSelect: vi.fn(),
      onSelectRange,
      onSetSelection: vi.fn(),
      onSetMonitored: vi.fn(),
    };
    const rows = [
      item({ id: 10, number: 1 }),
      item({ id: 20, number: 2 }),
      item({ id: 30, number: 3 }),
      item({ id: 40, number: 4 }),
    ];
    const { rerender } = render(
      <EpisodesTab detail={detail(rows)} {...props} />,
    );

    await user.click(
      screen.getByRole("checkbox", { name: /select episode 2/i }),
    );
    // Episode 1 arrives late, pushing every anchored index along by one.
    rerender(
      <EpisodesTab
        detail={detail([item({ id: 5, number: 0 }), ...rows])}
        {...props}
      />,
    );

    await user.keyboard("{Shift>}");
    await user.click(
      screen.getByRole("checkbox", { name: /select episode 4/i }),
    );
    await user.keyboard("{/Shift}");
    expect(onSelectRange).toHaveBeenCalledWith([20, 30, 40]);
  });
});

describe("EpisodesTab unmonitored status", () => {
  // "Wanted" is the one status that becomes false when an item is unmonitored;
  // nothing is wanting it. The others stay true and are left alone.
  it("replaces only the wanted badge", () => {
    renderStrip([
      item({ id: 1, number: 1, monitored: false, status: "wanted" }),
      item({
        id: 2,
        number: 2,
        monitored: false,
        status: "downloading",
        release_title: "[SynthSubs] Placeholder Saga - 02 [1080p]",
      }),
    ]);

    expect(screen.getByText("Not monitored")).toBeInTheDocument();
    expect(screen.queryByText("Wanted")).not.toBeInTheDocument();
    // An unmonitored episode with a grab in flight really is downloading.
    expect(screen.getByText("Downloading")).toBeInTheDocument();
  });

  // An unmonitored item the library holds keeps its true status; the row's
  // monitor toggle is what says it is unmonitored.
  it("leaves every other status alone when unmonitored", () => {
    renderStrip([
      item({
        id: 1,
        number: 1,
        monitored: false,
        in_library: true,
        status: "in_library",
      }),
      item({ id: 2, number: 2, monitored: false, status: "stuck" }),
      item({ id: 3, number: 3, monitored: false, status: "deferred" }),
    ]);

    expect(screen.getByText("In library")).toBeInTheDocument();
    expect(screen.getByText("Import blocked")).toBeInTheDocument();
    expect(screen.getByText("Batch downloaded")).toBeInTheDocument();
    expect(screen.queryByText("Not monitored")).not.toBeInTheDocument();
  });
});

describe("EpisodesTab select all", () => {
  // Replaces the selection rather than adding to it.
  it("selects every row and clears again from the header", async () => {
    const onSetSelection = vi.fn();
    const user = userEvent.setup();
    const items = [
      item({ id: 10, number: 1 }),
      item({ id: 20, number: 2 }),
      item({ id: 30, number: 3 }),
    ];
    const { rerender } = render(
      <EpisodesTab
        detail={detail(items)}
        onSearchAll={vi.fn()}
        onSearchItem={vi.fn()}
        selected={new Set()}
        onToggleSelect={vi.fn()}
        onSelectRange={vi.fn()}
        onSetSelection={onSetSelection}
        onSetMonitored={vi.fn()}
      />,
    );

    await user.click(screen.getByRole("checkbox", { name: /select all/i }));
    expect(onSetSelection).toHaveBeenCalledWith([10, 20, 30]);

    rerender(
      <EpisodesTab
        detail={detail(items)}
        onSearchAll={vi.fn()}
        onSearchItem={vi.fn()}
        selected={new Set([10, 20, 30])}
        onToggleSelect={vi.fn()}
        onSelectRange={vi.fn()}
        onSetSelection={onSetSelection}
        onSetMonitored={vi.fn()}
      />,
    );
    await user.click(screen.getByRole("checkbox", { name: /deselect all/i }));
    expect(onSetSelection).toHaveBeenLastCalledWith([]);
  });

  // A partial selection must not read as "none selected", which is what a bare
  // unchecked box would claim.
  it("reads as indeterminate on a partial selection", () => {
    render(
      <EpisodesTab
        detail={detail([
          item({ id: 10, number: 1 }),
          item({ id: 20, number: 2 }),
        ])}
        onSearchAll={vi.fn()}
        onSearchItem={vi.fn()}
        selected={new Set([10])}
        onToggleSelect={vi.fn()}
        onSelectRange={vi.fn()}
        onSetSelection={vi.fn()}
        onSetMonitored={vi.fn()}
      />,
    );

    // Radix models it natively as a third checked state, so it reaches
    // assistive tech as aria-checked="mixed" rather than as a DOM property.
    const box = screen.getByRole("checkbox", { name: /select all/i });
    expect(box).toHaveAttribute("aria-checked", "mixed");
    expect(box).toHaveAttribute("data-state", "indeterminate");
  });
});
