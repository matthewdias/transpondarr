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

// A film's date can be a date-only release held at noon UTC, so a countdown
// would invent precision — and "Released in 4h" is wrong on its own terms.
it("dates an upcoming film rather than counting down to it", () => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date("2026-03-15T08:00:00Z"));
  try {
    renderCard({ airs_at: "2026-03-15T12:00:00Z" });

    expect(screen.getByText(/^Premieres /)).toBeInTheDocument();
    expect(
      screen.queryByText(/\bin \d+[mhd]\b|any moment/),
    ).not.toBeInTheDocument();
    expect(screen.queryByText(/^Released /)).not.toBeInTheDocument();
  } finally {
    vi.useRealTimers();
  }
});

it("still says released once the date has passed", () => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date("2026-06-01T12:00:00Z"));
  try {
    renderCard({ airs_at: "2026-03-15T12:00:00Z" });

    expect(screen.getByText(/^Released /)).toBeInTheDocument();
  } finally {
    vi.useRealTimers();
  }
});

// Substituted rather than qualified, exactly as the episode row does it.
it("substitutes the unmonitored badge for a wanted film", () => {
  renderCard({ monitored: false });

  expect(screen.getByText("Not monitored")).toBeInTheDocument();
  expect(screen.queryByText("Wanted")).not.toBeInTheDocument();
});
