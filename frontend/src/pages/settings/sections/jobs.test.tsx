import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import {
  afterAll,
  afterEach,
  beforeAll,
  describe,
  expect,
  it,
  vi,
} from "vitest";
import type { JobStatus } from "@/lib/api";
import { JobsSection, JobsTable } from "@/pages/settings/sections/jobs";

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

  it("offers each job a run of its own", async () => {
    const onRun = vi.fn();
    render(
      <JobsTable jobs={[job(), job({ name: "import-scan" })]} onRun={onRun} />,
    );

    await userEvent.click(
      screen.getByRole("button", { name: "Run Wanted search now" }),
    );
    expect(onRun).toHaveBeenCalledExactlyOnceWith("wanted-search");
  });

  // A trigger during a run is not absorbed into it — the runner queues a full
  // second pass as soon as the run finishes — so the button refuses to stack one.
  it("cannot run a job that is already running", () => {
    render(<JobsTable jobs={[job({ running: true })]} onRun={vi.fn()} />);
    expect(
      screen.getByRole("button", { name: "Run Wanted search now" }),
    ).toBeDisabled();
  });

  // Trigger returns before the runner flips `running`, so until the poll
  // catches up the request in flight is the only sign a second click would
  // stack a second pass.
  it("holds every run button while a run request is in flight", () => {
    render(
      <JobsTable
        jobs={[job(), job({ name: "import-scan" })]}
        onRun={vi.fn()}
        busy
      />,
    );
    expect(
      screen.getByRole("button", { name: "Run Wanted search now" }),
    ).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "Run Import scan now" }),
    ).toBeDisabled();
  });

  it("leaves the run button out when nothing can act on it", () => {
    render(<JobsTable jobs={[job()]} />);
    expect(
      screen.queryByRole("button", { name: /^Run/ }),
    ).not.toBeInTheDocument();
  });
});

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

// runs collects every job name the section asked the server to run.
function renderSection(
  jobs: JobStatus[],
  automationEnabled: boolean | "unreadable",
) {
  const runs: string[] = [];
  server.use(
    http.get("/api/v1/system/jobs", () => HttpResponse.json({ jobs })),
    http.get("/api/v1/settings", () =>
      automationEnabled === "unreadable"
        ? new HttpResponse(null, { status: 500 })
        : HttpResponse.json({
            automation: { enabled: automationEnabled, pin_delay_hours: 0 },
          }),
    ),
    http.post("/api/v1/system/jobs/:name/run", ({ params }) => {
      runs.push(String(params.name));
      return new HttpResponse(null, { status: 202 });
    }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <JobsSection />
    </QueryClientProvider>,
  );
  return runs;
}

const runButton = (label: string) =>
  screen.findByRole("button", { name: `Run ${label} now` });

describe("JobsSection", () => {
  it("runs a job on request while automation is on", async () => {
    const runs = renderSection([job()], true);
    await userEvent.click(await runButton("Wanted search"));
    await waitFor(() => expect(runs).toEqual(["wanted-search"]));
  });

  // Running the sweep with the kill switch off grabs for real, so the one case
  // where the button contradicts a setting the user chose asks first.
  it("asks before running an automation-gated job with automation off", async () => {
    const runs = renderSection([job()], false);
    await userEvent.click(await runButton("Wanted search"));

    expect(await screen.findByRole("dialog")).toHaveTextContent(
      /automation is off/i,
    );
    expect(runs).toEqual([]);

    await userEvent.click(screen.getByRole("button", { name: /run anyway/i }));
    await waitFor(() => expect(runs).toEqual(["wanted-search"]));
  });

  // A failed settings read still asks (fail-safe), but must not assert a state
  // it never learned.
  it("admits when it could not tell whether automation is off", async () => {
    const runs = renderSection([job()], "unreadable");
    await userEvent.click(await runButton("Wanted search"));

    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent(/may be off/i);
    expect(dialog).not.toHaveTextContent(/automation is off/i);
    expect(runs).toEqual([]);
  });

  it("runs nothing when that confirmation is declined", async () => {
    const runs = renderSection([job()], false);
    await userEvent.click(await runButton("Wanted search"));
    await userEvent.click(screen.getByRole("button", { name: /cancel/i }));

    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
    expect(runs).toEqual([]);
  });

  // Only the two jobs that grab are gated; the rest are unaffected by the switch.
  it("does not ask about a job automation never gated", async () => {
    const runs = renderSection([job({ name: "session-cleanup" })], false);
    await userEvent.click(await runButton("Session cleanup"));

    await waitFor(() => expect(runs).toEqual(["session-cleanup"]));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });
});
