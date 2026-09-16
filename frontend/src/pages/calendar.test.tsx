import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter } from "react-router";
import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";
import type { CalendarItem, UnscheduledTitle } from "@/lib/api";
import { SidebarProvider } from "@/components/ui/sidebar";
import { CalendarPage } from "@/pages/calendar";

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

// Local noon today is today in every zone, so entries land on the rendered
// grid no matter where the test runs.
const todayNoon = () => {
  const d = new Date();
  d.setHours(12, 0, 0, 0);
  return d.toISOString();
};

const item = (over: Partial<CalendarItem>): CalendarItem => ({
  id: 1,
  title_id: 7,
  title: "Signal Anomaly",
  monitored: true,
  number: 4,
  format: "TV",
  airs_at: todayNoon(),
  status: "wanted",
  ...over,
});

const film = (over: Partial<CalendarItem> = {}): CalendarItem =>
  item({
    id: 9,
    title_id: 12,
    title: "Placeholder Legend",
    number: 1,
    format: "MOVIE",
    ...over,
  });

// A time anywhere on a film's entry would state precision we may have invented:
// a date-only premiere is stored at noon UTC to name a day, not a moment. A
// countdown counts as one, which is why this is wider than a clock pattern.
const statesATime = (el: HTMLElement) =>
  /\d{1,2}:\d{2}|\bin \d+[mhd]\b|any moment/.test(el.textContent ?? "");

const calendarHandler = (
  items: CalendarItem[],
  unscheduled: UnscheduledTitle[] = [],
  onCall?: () => void,
) =>
  http.get("/api/v1/calendar", () => {
    onCall?.();
    return HttpResponse.json({ items, unscheduled });
  });

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <SidebarProvider>
          <CalendarPage />
        </SidebarProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("CalendarPage", () => {
  it("renders today's episodes on the month grid and names unscheduled titles", async () => {
    server.use(
      calendarHandler(
        [item({})],
        [{ title_id: 103, title: "Dusty Archive", schedule_checked: true }],
      ),
    );

    renderPage();

    // Month view is the wide-screen default; the entry links to its title's page.
    const entry = await screen.findByRole("link", {
      name: /04 signal anomaly/i,
    });
    expect(entry).toHaveAttribute("href", "/titles/7");
    expect(entry).toHaveAttribute(
      "title",
      "Signal Anomaly — episode 4 (Wanted)",
    );

    // A title with no schedule data is surfaced, not silently omitted.
    expect(screen.getByText(/no schedule data/i)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Dusty Archive" })).toHaveAttribute(
      "href",
      "/titles/103",
    );
  });

  it("does not report a provider published nothing for a title it never asked about", async () => {
    server.use(
      calendarHandler(
        [item({})],
        [
          { title_id: 103, title: "Dusty Archive", schedule_checked: true },
          { title_id: 104, title: "Fresh Arrival", schedule_checked: false },
        ],
      ),
    );

    renderPage();

    // The settled absence keeps its wording and names only the asked-about title.
    const settled = await screen.findByText(/no schedule data/i);
    const settledRow = settled.closest("div")!;
    expect(
      within(settledRow).getByRole("link", { name: "Dusty Archive" }),
    ).toBeInTheDocument();
    expect(
      within(settledRow).queryByRole("link", { name: "Fresh Arrival" }),
    ).not.toBeInTheDocument();

    // The unasked one is surfaced too, but as pending rather than as a verdict.
    const pending = screen.getByText(/not checked yet/i);
    const pendingRow = pending.closest("div")!;
    expect(
      within(pendingRow).getByRole("link", { name: "Fresh Arrival" }),
    ).toHaveAttribute("href", "/titles/104");
  });

  it("shows agenda rows with the status badge and import error", async () => {
    server.use(
      calendarHandler([
        item({}),
        item({
          id: 2,
          title_id: 8,
          title: "Backlog Kaiju",
          number: 1,
          status: "stuck",
          import_error: "library offline",
        }),
      ]),
    );

    renderPage();
    await screen.findByRole("link", { name: /signal anomaly/i });
    await userEvent.click(screen.getByRole("tab", { name: "Agenda" }));

    expect(await screen.findByText(/· today/i)).toBeInTheDocument();
    expect(screen.getByText("Wanted")).toBeInTheDocument();
    // A badge inside a link can't be a button, so the error is shown as text.
    const stuck = screen.getByRole("link", { name: /backlog kaiju/i });
    expect(stuck).toHaveTextContent("Import blocked");
    expect(stuck).toHaveTextContent("library offline");

    await userEvent.click(screen.getByRole("tab", { name: "Week" }));
    const week = await screen.findByRole("link", { name: /backlog kaiju/i });
    expect(week).toHaveTextContent("library offline");
    expect(screen.queryByTitle("library offline")).toBeNull();
  });

  // Colour alone can't convey status (WCAG 1.4.1): each month entry names it in
  // words for a screen reader and marks it with a glyph whose shape differs.
  it("marks a month entry's status by shape and in words, not by colour alone", async () => {
    server.use(
      calendarHandler([
        item({ id: 1, number: 1, status: "in_library" }),
        item({ id: 2, number: 2, status: "downloading" }),
        item({ id: 3, number: 3, status: "deferred" }),
        item({ id: 4, number: 4, status: "stuck" }),
        item({ id: 5, number: 5, status: "wanted" }),
      ]),
    );

    renderPage();

    const words = [
      "In library",
      "Downloading",
      "Batch downloaded",
      "Import blocked",
      "Wanted",
    ];
    const shapes = new Set<string>();
    for (const [i, word] of words.entries()) {
      const entry = await screen.findByRole("link", {
        name: new RegExp(`0${i + 1} signal anomaly\\W+${word}$`, "i"),
      });
      const marker = entry.querySelector("[data-status-marker]");
      expect(marker, word).not.toBeNull();
      expect(marker).toHaveAttribute("aria-hidden", "true");
      shapes.add(
        marker!.tagName === "svg"
          ? [...marker!.classList].filter((c) => c.startsWith("lucide-")).join()
          : "dot",
      );
    }
    expect(shapes.size).toBe(words.length);
  });

  it("renders a film as a premiere, not as episode 1, in every view", async () => {
    server.use(calendarHandler([film()]));

    renderPage();

    const entry = await screen.findByRole("link", {
      name: /placeholder legend/i,
    });
    expect(entry).toHaveAttribute("href", "/titles/12");
    expect(entry).toHaveAttribute(
      "title",
      "Placeholder Legend — premiere (Wanted)",
    );
    expect(entry).toHaveTextContent(/^Placeholder Legend, Wanted$/);

    await userEvent.click(screen.getByRole("tab", { name: "Week" }));
    const week = await screen.findByRole("link", {
      name: /placeholder legend/i,
    });
    expect(week).toHaveTextContent(/Premiere/);
    expect(week).not.toHaveTextContent(/Ep\s/);
    expect(statesATime(week)).toBe(false);

    await userEvent.click(screen.getByRole("tab", { name: "Agenda" }));
    const agenda = await screen.findByRole("link", {
      name: /placeholder legend/i,
    });
    expect(agenda).toHaveTextContent(/Premiere/);
    expect(agenda).not.toHaveTextContent(/Ep\s/);
    expect(statesATime(agenda)).toBe(false);
  });

  // A film's deferral is a size tie or an unextracted archive (#210), never a
  // batch, so neither the compact label (week) nor the badge (agenda) may show
  // one. The two renderers word it separately, so both are asserted here.
  it("shows a deferred film as not imported, never as a batch", async () => {
    server.use(calendarHandler([film({ status: "deferred" })]));

    renderPage();
    await screen.findByRole("link", { name: /placeholder legend/i });

    await userEvent.click(screen.getByRole("tab", { name: "Week" }));
    const week = await screen.findByRole("link", {
      name: /placeholder legend/i,
    });
    expect(week).toHaveTextContent(/Downloaded, not imported/);
    expect(week).not.toHaveTextContent(/Batch downloaded/);

    await userEvent.click(screen.getByRole("tab", { name: "Agenda" }));
    const agenda = await screen.findByRole("link", {
      name: /placeholder legend/i,
    });
    expect(agenda).toHaveTextContent(/Downloaded, not imported/);
    expect(agenda).not.toHaveTextContent(/Batch downloaded/);
  });

  // #210's rule: only the strings shown for a film change wording.
  it("keeps the batch wording for a deferred episode in both views", async () => {
    server.use(calendarHandler([item({ status: "deferred" })]));

    renderPage();
    await screen.findByRole("link", { name: /signal anomaly/i });

    await userEvent.click(screen.getByRole("tab", { name: "Week" }));
    expect(
      await screen.findByRole("link", { name: /signal anomaly/i }),
    ).toHaveTextContent(/Batch downloaded/);

    await userEvent.click(screen.getByRole("tab", { name: "Agenda" }));
    expect(
      await screen.findByRole("link", { name: /signal anomaly/i }),
    ).toHaveTextContent(/Batch downloaded/);
  });

  // Format is the sole discriminator (#208): a one-item OVA is series-shaped.
  it("keeps the episode line for a single-item OVA", async () => {
    server.use(
      calendarHandler([
        film({ id: 3, title_id: 21, title: "Quiet Interlude", format: "OVA" }),
      ]),
    );

    renderPage();

    const entry = await screen.findByRole("link", {
      name: /01 quiet interlude/i,
    });
    expect(entry).toHaveAttribute(
      "title",
      "Quiet Interlude — episode 1 (Wanted)",
    );
  });

  it("names the empty week without the word episodes", async () => {
    server.use(calendarHandler([]));

    renderPage();
    await userEvent.click(await screen.findByRole("tab", { name: "Agenda" }));

    expect(
      await screen.findByText("Nothing monitored is scheduled this week."),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Nothing scheduled" }),
    ).toBeInTheDocument();
  });

  it("reports a calendar that could not be loaded, with a retry", async () => {
    server.use(
      http.get("/api/v1/calendar", () =>
        HttpResponse.json(
          { status: 500, detail: "database is locked" },
          { status: 500 },
        ),
      ),
    );

    renderPage();

    expect(
      await screen.findByText("Couldn’t load the calendar. database is locked"),
    ).toBeInTheDocument();

    server.use(calendarHandler([item({})]));
    await userEvent.click(screen.getByRole("button", { name: /try again/i }));
    expect(await screen.findByText("Signal Anomaly")).toBeInTheDocument();
    expect(
      screen.queryByText(/Couldn’t load the calendar/),
    ).not.toBeInTheDocument();
  });

  it("points each selected view tab at the panel it shows", async () => {
    server.use(calendarHandler([item({})]));

    renderPage();
    await screen.findByRole("link", { name: /signal anomaly/i });

    for (const view of ["Agenda", "Week", "Month"]) {
      await userEvent.click(screen.getByRole("tab", { name: view }));
      const tab = screen.getByRole("tab", { name: view, selected: true });
      const panel = document.getElementById(
        tab.getAttribute("aria-controls") ?? "",
      );
      expect(panel, view).toHaveAttribute("role", "tabpanel");
      expect(panel).toHaveAccessibleName(view);
      expect(
        within(panel!).getByRole("link", { name: /signal anomaly/i }),
      ).toBeInTheDocument();
    }
  });

  it("defaults to the agenda view on a narrow screen with a single fetch", async () => {
    const wide = window.innerWidth;
    window.innerWidth = 375;
    try {
      let calls = 0;
      server.use(calendarHandler([item({})], [], () => calls++));

      renderPage();

      // Agenda rows show a leading time column; the month grid never mounts,
      // so exactly one range is fetched.
      expect(await screen.findByText(/· today/i)).toBeInTheDocument();
      expect(
        screen.getByRole("tab", { name: "Agenda", selected: true }),
      ).toBeInTheDocument();
      expect(calls).toBe(1);
    } finally {
      window.innerWidth = wide;
    }
  });

  // From 768px the sidebar takes 256px in flow, leaving a ~512px column where the
  // month grid shortened every episode to a bullet and a number (#320).
  it("defaults to the agenda view when the sidebar narrows a 768px viewport", async () => {
    const wide = window.innerWidth;
    window.innerWidth = 768;
    try {
      let calls = 0;
      server.use(calendarHandler([item({})], [], () => calls++));

      renderPage();

      expect(await screen.findByText(/\u00b7 today/i)).toBeInTheDocument();
      expect(
        screen.getByRole("tab", { name: "Agenda", selected: true }),
      ).toBeInTheDocument();
      expect(calls).toBe(1);
    } finally {
      window.innerWidth = wide;
    }
  });
});
