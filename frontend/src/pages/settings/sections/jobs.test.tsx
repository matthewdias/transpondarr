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

describe("JobsTable", () => {
  it("reads a job's telemetry back in the units it was reported in", () => {
    const minuteAgo = new Date(Date.now() - 60_000).toISOString();
    render(
      <JobsTable
        jobs={[
          job({
            name: "wanted-search",
            last_run: minuteAgo,
            last_duration_ms: 1240,
          }),
          job({
            name: "import-scan",
            last_run: minuteAgo,
            last_duration_ms: 0.5,
          }),
          job({
            name: "session-cleanup",
            last_run: minuteAgo,
            last_duration_ms: 4.667,
          }),
          job({
            name: "airing-sync",
            last_run: minuteAgo,
            last_duration_ms: 12.4,
          }),
        ]}
      />,
    );

    // The runner's names are kebab-case identifiers; the card is prose.
    expect(rowFor("Wanted search")).toBeInTheDocument();
    expect(rowFor("Import scan")).toBeInTheDocument();

    expect(
      within(rowFor("Wanted search")).getByText("1.2 s"),
    ).toBeInTheDocument();
    // Sub-millisecond runs are why the DTO is fractional; rounding them to "0 ms"
    // here would throw that away again. Every larger scale sheds the noise.
    expect(
      within(rowFor("Import scan")).getByText("0.5 ms"),
    ).toBeInTheDocument();
    expect(
      within(rowFor("Session cleanup")).getByText("4.7 ms"),
    ).toBeInTheDocument();
    expect(
      within(rowFor("Airing sync")).getByText("12 ms"),
    ).toBeInTheDocument();
    expect(
      within(rowFor("Wanted search")).getByText("1m ago"),
    ).toBeInTheDocument();
  });

  it("says a job has never run rather than rendering an unset timestamp", () => {
    render(<JobsTable jobs={[job()]} />);
    const row = rowFor("Wanted search");
    expect(within(row).getByText("Never")).toBeInTheDocument();
    expect(
      within(row).queryByText(/1970|ms|NaN|Invalid/),
    ).not.toBeInTheDocument();
  });

  it("surfaces a job's last error, styled as an error", () => {
    render(
      <JobsTable
        jobs={[
          job({
            last_run: new Date().toISOString(),
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
        jobs={[job({ running: true, last_run: new Date().toISOString() })]}
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
