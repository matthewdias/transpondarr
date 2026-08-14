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

// Format is the discriminator (#208) and it guarantees one item, so a film's
// row shows that item's own state -- the count it replaces is 0 / 1 either way.
it.each([
  ["in_library", "In library"],
  ["downloading", "Downloading"],
  ["deferred", "Downloaded, not imported"],
  ["stuck", "Import blocked"],
  ["wanted", "Wanted"],
] as const)("shows a film's %s state rather than a count", (status, label) => {
  render(
    <LibraryProgress
      format="MOVIE"
      inLibrary={status === "in_library" ? 1 : 0}
      tracked={1}
      monitored={1}
      total={1}
      status={status}
    />,
  );

  expect(screen.getByText(label)).toBeInTheDocument();
  expect(screen.queryByText(/\d+ \/ \d+/)).not.toBeInTheDocument();
});

// The defect: every one of these was "not held", so the interim had to fall
// back to 0 / 1 and a downloading film was indistinguishable from a wanted one.
it("distinguishes a downloading film from a wanted one", () => {
  render(
    <LibraryProgress
      format="MOVIE"
      inLibrary={0}
      tracked={1}
      monitored={1}
      total={1}
      status="downloading"
    />,
  );

  expect(screen.queryByText("Wanted")).not.toBeInTheDocument();
});

it("hangs the import reason off the blocked film", () => {
  render(
    <LibraryProgress
      format="MOVIE"
      inLibrary={0}
      tracked={1}
      monitored={1}
      total={1}
      status="stuck"
      importError="no movies root configured"
    />,
  );

  expect(screen.getByText("Import blocked")).toHaveAttribute(
    "title",
    "no movies root configured",
  );
});

// Substituted, not qualified, exactly as the detail page does it: every other
// status stays true when unmonitored, and only "Wanted" becomes a false claim.
it("names monitoring instead of wanting an unmonitored film", () => {
  render(
    <LibraryProgress
      format="MOVIE"
      inLibrary={0}
      tracked={0}
      monitored={0}
      total={1}
      status="wanted"
    />,
  );

  expect(screen.getByText("Not monitored")).toBeInTheDocument();
  expect(screen.queryByText("Wanted")).not.toBeInTheDocument();
});

it("keeps a film's real state when it is unmonitored", () => {
  render(
    <LibraryProgress
      format="MOVIE"
      inLibrary={0}
      tracked={0}
      monitored={0}
      total={1}
      status="deferred"
    />,
  );

  expect(screen.getByText("Downloaded, not imported")).toBeInTheDocument();
  expect(screen.queryByText("Not monitored")).not.toBeInTheDocument();
});

// A film with no item at all publishes no state; the shared count path is the
// answer, not a movie-shaped guess at one.
it("takes the count path for a film with no state to report", () => {
  render(
    <LibraryProgress
      format="MOVIE"
      inLibrary={0}
      tracked={0}
      monitored={0}
      total={0}
    />,
  );

  expect(screen.getByText("0 / 0")).toBeInTheDocument();
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
