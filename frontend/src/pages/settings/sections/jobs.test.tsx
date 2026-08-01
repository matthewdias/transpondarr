import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { JobStatus } from "@/lib/api";
import { JobsTable } from "@/pages/settings/sections/jobs";

function job(over: Partial<JobStatus> = {}): JobStatus {
  return {
    name: "wanted-search",
    interval_ms: 900_000,
    running: false,
    last_duration_ms: 0,
    ...over,
  };
}

function rowFor(name: string) {
  return screen.getByRole("listitem", { name });
}

const minutesFromNow = (n: number) =>
  new Date(Date.now() + n * 60_000).toISOString();

describe("JobsTable", () => {
  it("reads a job's schedule back on both sides of now", () => {
    render(
      <JobsTable
        jobs={[
          // 90m rather than a round quarter-hour: the countdown floors, so a
          // value sitting on a unit boundary renders one unit short by the time
          // the assertion runs.
          job({
            name: "wanted-search",
            last_run: minutesFromNow(-1),
            next_run: minutesFromNow(90),
          }),
          job({ name: "import-scan", last_run: minutesFromNow(-120) }),
        ]}
      />,
    );

    // The runner's names are kebab-case identifiers; the card is prose.
    expect(rowFor("Wanted search")).toBeInTheDocument();
    expect(rowFor("Import scan")).toBeInTheDocument();

    const swept = within(rowFor("Wanted search"));
    expect(swept.getByText("1m ago")).toBeInTheDocument();
    expect(swept.getByText("in 1h")).toBeInTheDocument();

    // Absent while the runner is not running, so it must not render as an instant.
    expect(
      within(rowFor("Import scan")).getByText("2h ago"),
    ).toBeInTheDocument();
  });

  // The failure #110 exists to catch: the runner stopped scheduling, which a
  // relative last-run alone cannot show without knowing the job's interval.
  it("calls a job whose next run has passed overdue", () => {
    render(
      <JobsTable
        jobs={[
          job({ last_run: minutesFromNow(-60), next_run: minutesFromNow(-45) }),
        ]}
      />,
    );
    const overdue = within(rowFor("Wanted search")).getByText("overdue");
    expect(overdue).toBeInTheDocument();
    expect(overdue).toHaveClass("text-destructive");
  });

  // The snapshot this renders from is up to one poll old, so a short-interval
  // job (import-scan runs every 15s) always shows a next run that has just
  // passed. Calling that overdue would flag a healthy runner on every poll.
  it("does not call a job overdue while the snapshot could simply be stale", () => {
    render(
      <JobsTable
        jobs={[
          job({
            interval_ms: 15_000,
            last_run: minutesFromNow(-0.2),
            next_run: minutesFromNow(-0.1),
          }),
        ]}
      />,
    );
    expect(
      within(rowFor("Wanted search")).queryByText("overdue"),
    ).not.toBeInTheDocument();
  });

  // The runner publishes nextRun *before* a run starts, so a job outlasting its
  // own interval has a past next_run while it is perfectly healthy.
  it("does not call a running job overdue when it has outlasted its interval", () => {
    render(
      <JobsTable
        jobs={[
          job({
            running: true,
            last_run: minutesFromNow(-30),
            next_run: minutesFromNow(-15),
          }),
        ]}
      />,
    );
    const row = within(rowFor("Wanted search"));
    expect(row.queryByText("overdue")).not.toBeInTheDocument();
    expect(row.getByText("Running")).toBeInTheDocument();
  });

  it("says a job has never run rather than rendering an unset timestamp", () => {
    render(<JobsTable jobs={[job({ next_run: minutesFromNow(5) })]} />);
    const row = rowFor("Wanted search");
    expect(within(row).getByText("Never")).toBeInTheDocument();
    expect(within(row).queryByText(/1970|NaN|Invalid/)).not.toBeInTheDocument();
  });

  // Duration is a developer's metric with no baseline on this card, so it keeps
  // its precision but gives up the column that next run earns.
  it("keeps the last duration available without spending a column on it", () => {
    render(
      <JobsTable
        jobs={[job({ last_run: minutesFromNow(-1), last_duration_ms: 0.5 })]}
      />,
    );
    expect(within(rowFor("Wanted search")).getByText("1m ago")).toHaveAttribute(
      "title",
      "Last run took 0.5 ms",
    );
  });

  it("surfaces a job's last error, styled as an error", () => {
    render(
      <JobsTable
        jobs={[
          job({
            last_run: minutesFromNow(-1),
            last_error: "indexer search failed",
          }),
        ]}
      />,
    );
    const err = screen.getByText("indexer search failed");
    expect(err).toBeInTheDocument();
    expect(err).toHaveClass("text-destructive");
  });

  it("marks a job that is running right now", () => {
    render(
      <JobsTable
        jobs={[job({ running: true, last_run: minutesFromNow(0) })]}
      />,
    );
    expect(
      within(rowFor("Wanted search")).getByText("Running"),
    ).toBeInTheDocument();
  });

  it("explains an empty runner instead of rendering a bare empty list", () => {
    render(<JobsTable jobs={[]} />);
    expect(screen.getByText(/no background jobs/i)).toBeInTheDocument();
  });
});
