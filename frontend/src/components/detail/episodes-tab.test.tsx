import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { EpisodesTab } from "@/components/detail/episodes-tab";
import type { SeriesDetail, WantedItem } from "@/lib/api";

const item = (over: Partial<WantedItem>): WantedItem => ({
  id: 1,
  number: 1,
  in_library: false,
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
