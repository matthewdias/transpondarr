import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter } from "react-router-dom";
import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";
import type { SeasonEntry } from "@/lib/api";
import { SidebarProvider } from "@/components/ui/sidebar";
import { DiscoveryPage } from "@/pages/discovery";

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const entry = (over: Partial<SeasonEntry>): SeasonEntry => ({
  anilist_id: 1,
  episodes: 12,
  genres: [],
  average_score: 0,
  tracked: false,
  ...over,
});

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <SidebarProvider>
          <DiscoveryPage />
        </SidebarProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

const chartHandler = (entries: SeasonEntry[]) =>
  http.get("/api/v1/browse/season", ({ request }) => {
    const url = new URL(request.url);
    return HttpResponse.json({
      season: url.searchParams.get("season"),
      year: Number(url.searchParams.get("year")),
      entries,
    });
  });

describe("DiscoveryPage", () => {
  it("renders the chart with tracked entries marked and countdowns clamped", async () => {
    const inTwoDays = new Date(Date.now() + 2.5 * 86400_000).toISOString();
    const threeHoursAgo = new Date(Date.now() - 3 * 3600_000).toISOString();
    server.use(
      chartHandler([
        entry({
          anilist_id: 101,
          romaji: "Alpha Adventure",
          format: "TV",
          status: "RELEASING",
          average_score: 78,
          studio: "Studio A",
          next_episode: 6,
          next_airs_at: inTwoDays,
        }),
        entry({
          anilist_id: 102,
          romaji: "Beta Ballad",
          format: "TV",
          status: "RELEASING",
          tracked: true,
          series_id: 7,
          next_episode: 9,
          next_airs_at: threeHoursAgo,
        }),
      ]),
    );

    renderPage();

    expect(await screen.findByText("Alpha Adventure")).toBeInTheDocument();
    expect(screen.getByText(/78%/)).toBeInTheDocument();
    expect(screen.getByText("Ep 6 in 2d")).toBeInTheDocument();

    // The stale cached timestamp clamps to "aired", never a negative countdown.
    expect(screen.getByText("Ep 9 aired")).toBeInTheDocument();

    // The tracked entry offers its library page, not a second add.
    const inLibrary = screen.getByRole("link", { name: /in library/i });
    expect(inLibrary).toHaveAttribute("href", "/series/7");
    expect(screen.getAllByRole("button", { name: /^add$/i })).toHaveLength(1);
  });

  it("adds a show in place and re-marks it as tracked", async () => {
    let added = false;
    server.use(
      http.get("/api/v1/browse/season", () =>
        HttpResponse.json({
          season: "summer",
          year: 2026,
          entries: [
            entry({
              anilist_id: 101,
              romaji: "Alpha Adventure",
              tracked: added,
              series_id: added ? 9 : undefined,
            }),
          ],
        }),
      ),
      http.post("/api/v1/series", async () => {
        added = true;
        return HttpResponse.json(
          { id: 9, title: "Alpha Adventure", monitored: true, items: [] },
          { status: 201 },
        );
      }),
      // The add flow invalidates the series list, which the sidebar-less test
      // page never queries; tolerate it if something does.
      http.get("/api/v1/series", () => HttpResponse.json({ series: [] })),
    );

    renderPage();

    const card = (await screen.findByText("Alpha Adventure")).closest(
      "div",
    )!.parentElement!;
    await userEvent.click(within(card).getByRole("button", { name: /add/i }));

    expect(
      await screen.findByRole("link", { name: /in library/i }),
    ).toHaveAttribute("href", "/series/9");
  });

  it("keeps a movie visible but not addable", async () => {
    server.use(
      chartHandler([
        entry({ anilist_id: 103, romaji: "Gamma the Movie", format: "MOVIE" }),
      ]),
    );

    renderPage();

    expect(await screen.findByText("Gamma the Movie")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /add/i })).toBeDisabled();
  });
});
