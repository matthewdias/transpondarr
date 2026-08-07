import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { LibraryProgress } from "@/components/library-progress";

// The denominator is what the series is pursuing (monitored and aired), so it
// is routinely a subset -- and the raw count has to stay beside it.
it("shows the raw total when the denominator is a subset", () => {
  render(<LibraryProgress inLibrary={3} tracked={3} total={12} />);

  expect(screen.getByText("3 / 3")).toBeInTheDocument();
  expect(screen.getByText("(12 total)")).toBeInTheDocument();
});

it("drops the total when the two agree", () => {
  render(<LibraryProgress inLibrary={3} tracked={12} total={12} />);

  expect(screen.getByText("3 / 12")).toBeInTheDocument();
  expect(screen.queryByText(/total/)).not.toBeInTheDocument();
});

it("words the zero state rather than reading as an empty series", () => {
  render(<LibraryProgress inLibrary={0} tracked={0} total={12} />);

  expect(screen.getByText("Nothing aired yet")).toBeInTheDocument();
  expect(screen.queryByText("0 / 0")).not.toBeInTheDocument();
  expect(screen.getByText("(12 total)")).toBeInTheDocument();
});

// A series that genuinely has no items is the one case "0 / 0" states honestly.
it("keeps the ratio for a series with no items at all", () => {
  render(<LibraryProgress inLibrary={0} tracked={0} total={0} />);

  expect(screen.getByText("0 / 0")).toBeInTheDocument();
  expect(screen.queryByText("Nothing aired yet")).not.toBeInTheDocument();
});
