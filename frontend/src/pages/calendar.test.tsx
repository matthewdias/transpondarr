import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter } from "react-router";
import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";
import type { CalendarItem, UnscheduledSeries } from "@/lib/api";
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
  unscheduled: UnscheduledSeries[] = [],
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
  it("renders today's episodes on the month grid and names unscheduled series", async () => {
    server.use(
      calendarHandler([item({})], [{ title_id: 103, title: "Dusty Archive" }]),
    );

    renderPage();

    // Month view is the wide-screen default; the entry links to its series.
    const entry = await screen.findByRole("link", {
      name: /04 signal anomaly/i,
    });
    expect(entry).toHaveAttribute("href", "/series/7");
    expect(entry).toHaveAttribute(
      "title",
      "Signal Anomaly — episode 4 (wanted)",
    );

    // A series with no schedule data is surfaced, not silently omitted.
    expect(screen.getByText(/no schedule data/i)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Dusty Archive" })).toHaveAttribute(
      "href",
      "/series/103",
    );
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
    // The stuck badge carries the import error as its tooltip.
    expect(screen.getByText("Import blocked")).toHaveAccessibleDescription();
    expect(screen.getByTitle("library offline")).toBeInTheDocument();
  });

  it("renders a film as a premiere, not as episode 1, in every view", async () => {
    server.use(calendarHandler([film()]));

    renderPage();

    const entry = await screen.findByRole("link", {
      name: /placeholder legend/i,
    });
    expect(entry).toHaveAttribute("href", "/series/12");
    expect(entry).toHaveAttribute(
      "title",
      "Placeholder Legend — premiere (wanted)",
    );
    expect(entry).toHaveTextContent(/^Placeholder Legend$/);

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
      "Quiet Interlude — episode 1 (wanted)",
    );
  });

  it("names the empty week without assuming episodes", async () => {
    server.use(calendarHandler([]));

    renderPage();
    await userEvent.click(await screen.findByRole("tab", { name: "Agenda" }));

    expect(
      await screen.findByText("Nothing monitored is scheduled this week."),
    ).toBeInTheDocument();
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
});
