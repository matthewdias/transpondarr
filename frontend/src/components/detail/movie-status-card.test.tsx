import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import type { WantedItem } from "@/lib/api";
import { MovieStatusCard } from "@/components/detail/movie-status-card";

const item = (over: Partial<WantedItem> = {}): WantedItem => ({
  id: 3,
  number: 1,
  in_library: false,
  monitored: true,
  status: "wanted",
  ...over,
});

function renderCard(over: Partial<WantedItem> = {}, on = { search: vi.fn() }) {
  const user = userEvent.setup();
  render(
    <MovieStatusCard
      item={item(over)}
      onSearch={on.search}
      onSetMonitored={vi.fn()}
    />,
  );
  return user;
}

// The card stands in for the episodes table, so it must carry the state that
// table's row carried -- and never the episode number, which is the whole point.
it("reads the film's own state without naming an episode", async () => {
  renderCard({ status: "in_library", in_library: true, release_title: "REL" });

  expect(screen.getByText("In library")).toBeInTheDocument();
  expect(screen.getByText("REL")).toBeInTheDocument();
  expect(screen.queryByText(/episode/i)).not.toBeInTheDocument();
  expect(screen.queryByText(/^0?1$/)).not.toBeInTheDocument();
});

it("offers a search that hands off to the Releases tab", async () => {
  const search = vi.fn();
  const user = renderCard({}, { search });

  expect(screen.getByText("Wanted")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: /search/i }));
  expect(search).toHaveBeenCalled();
});

// Substituted rather than qualified, exactly as the episode row does it.
it("substitutes the unmonitored badge for a wanted film", () => {
  renderCard({ monitored: false });

  expect(screen.getByText("Not monitored")).toBeInTheDocument();
  expect(screen.queryByText("Wanted")).not.toBeInTheDocument();
});
