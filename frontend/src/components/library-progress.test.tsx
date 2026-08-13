import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { LibraryProgress } from "@/components/library-progress";

// The denominator is what the series is pursuing (monitored and aired), so it
// is routinely a subset -- and the raw count has to stay beside it.
it("shows the raw total when the denominator is a subset", () => {
  render(
    <LibraryProgress
      format="TV"
      inLibrary={3}
      tracked={3}
      monitored={12}
      total={12}
    />,
  );

  expect(screen.getByText("3 / 3")).toBeInTheDocument();
  expect(screen.getByText("(12 total)")).toBeInTheDocument();
});

it("drops the total when the two agree", () => {
  render(
    <LibraryProgress
      format="TV"
      inLibrary={3}
      tracked={12}
      monitored={12}
      total={12}
    />,
  );

  expect(screen.getByText("3 / 12")).toBeInTheDocument();
  expect(screen.queryByText(/total/)).not.toBeInTheDocument();
});

it("words the zero state rather than reading as an empty series", () => {
  render(
    <LibraryProgress
      format="TV"
      inLibrary={0}
      tracked={0}
      monitored={12}
      total={12}
    />,
  );

  expect(screen.getByText("Nothing aired yet")).toBeInTheDocument();
  expect(screen.queryByText("0 / 0")).not.toBeInTheDocument();
  expect(screen.getByText("(12 total)")).toBeInTheDocument();
});

// The same empty denominator, the other cause. Saying "nothing aired" over a
// finished series someone switched off is a plain false statement.
it("names monitoring, not airing, when nothing is monitored", () => {
  render(
    <LibraryProgress
      format="TV"
      inLibrary={0}
      tracked={0}
      monitored={0}
      total={12}
    />,
  );

  expect(screen.getByText("Nothing monitored")).toBeInTheDocument();
  expect(screen.queryByText("Nothing aired yet")).not.toBeInTheDocument();
  expect(screen.getByText("(12 total)")).toBeInTheDocument();
});

// A series that genuinely has no items is the one case "0 / 0" states honestly.
it("keeps the ratio for a series with no items at all", () => {
  render(
    <LibraryProgress
      format="TV"
      inLibrary={0}
      tracked={0}
      monitored={0}
      total={0}
    />,
  );

  expect(screen.getByText("0 / 0")).toBeInTheDocument();
  expect(screen.queryByText(/^Nothing/)).not.toBeInTheDocument();
});

// Format is the discriminator (#208), but the list DTO carries counts and not
// item status, so only "held" is knowable here: downloading, deferred and
// import-blocked all look like inLibrary 0. #215 brings the real state.
it("says a held film is in the library", () => {
  render(
    <LibraryProgress
      format="MOVIE"
      inLibrary={1}
      tracked={1}
      monitored={1}
      total={1}
    />,
  );

  expect(screen.getByText("In library")).toBeInTheDocument();
});

// "Wanted" would be a claim about a film that may be downloading right now; the
// count makes no claim at all, which is the honest fallback until #215.
it("falls back to the count rather than guessing why a film is not held", () => {
  render(
    <LibraryProgress
      format="MOVIE"
      inLibrary={0}
      tracked={1}
      monitored={1}
      total={1}
    />,
  );

  expect(screen.getByText("0 / 1")).toBeInTheDocument();
  expect(screen.queryByText("Wanted")).not.toBeInTheDocument();
});

it("keeps the count for a single-episode OVA", () => {
  render(
    <LibraryProgress
      format="OVA"
      inLibrary={0}
      tracked={1}
      monitored={1}
      total={1}
    />,
  );

  expect(screen.getByText("0 / 1")).toBeInTheDocument();
});
