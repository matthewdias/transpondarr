import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter } from "react-router";
import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";
import type { SeasonEntry } from "@/lib/api";
import { currentSeason, seasonLabel } from "@/lib/season";
import { SidebarProvider } from "@/components/ui/sidebar";
import { DiscoveryPage } from "@/pages/discovery";

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const entry = (over: Partial<SeasonEntry>): SeasonEntry => ({
  provider: "anilist",
  provider_id: 1,
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

const profilesHandler = http.get("/api/v1/profiles", () =>
  HttpResponse.json({
    profiles: [{ id: 1, name: "Default", is_default: true }],
  }),
);

// The add form reads the movies root for a film, so a movie card fetches this.
const settingsHandler = http.get("/api/v1/settings", () =>
  HttpResponse.json({
    automation: { mode: "on" },
    library: {
      dir: "/media/shows",
      movies_dir: "/media/films",
      mode: "hardlink",
      configured: true,
    },
  }),
);

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
      profilesHandler,
      chartHandler([
        entry({
          provider_id: 101,
          romaji: "Alpha Adventure",
          format: "TV",
          status: "RELEASING",
          average_score: 78,
          studio: "Studio A",
          next_episode: 6,
          next_airs_at: inTwoDays,
          description: "A hero rises.<br><i>Adapted from the manga.</i>",
        }),
        entry({
          provider_id: 102,
          romaji: "Beta Ballad",
          format: "TV",
          status: "RELEASING",
          tracked: true,
          title_id: 7,
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
    expect(inLibrary).toHaveAttribute("href", "/titles/7");
    expect(screen.getAllByRole("button", { name: /^add$/i })).toHaveLength(1);

    // The synopsis lives in the detail dialog, not on the card, so an expanded
    // description can never reflow the grid.
    expect(
      screen.queryByText("A hero rises. Adapted from the manga."),
    ).not.toBeInTheDocument();
    await userEvent.click(
      screen.getByRole("button", { name: /alpha adventure/i }),
    );
    const dialog = await screen.findByRole("dialog");
    expect(
      within(dialog).getByText("A hero rises. Adapted from the manga."),
    ).toBeInTheDocument();
    // The dialog carries the same actions as its card.
    expect(
      within(dialog).getByRole("link", { name: /open on anilist/i }),
    ).toHaveAttribute("href", "https://anilist.co/anime/101");

    // Adding from the detail hands over to the add form rather than stacking a
    // second dialog on top of the first.
    await userEvent.click(
      within(dialog).getByRole("button", { name: /^add$/i }),
    );
    expect(
      await screen.findByRole("combobox", { name: "Monitor" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("A hero rises. Adapted from the manga."),
    ).not.toBeInTheDocument();
    await userEvent.keyboard("{Escape}");

    // Every card links out to its AniList page.
    const anilist = screen.getAllByRole("link", { name: /open on anilist/i });
    expect(anilist).toHaveLength(2);
    expect(anilist[0]).toHaveAttribute("href", "https://anilist.co/anime/101");
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
              provider_id: 101,
              romaji: "Alpha Adventure",
              tracked: added,
              title_id: added ? 9 : undefined,
            }),
          ],
        }),
      ),
      http.post("/api/v1/titles", async () => {
        added = true;
        return HttpResponse.json(
          { id: 9, title: "Alpha Adventure", monitored: true, items: [] },
          { status: 201 },
        );
      }),
      // The add flow invalidates the series list, which the sidebar-less test
      // page never queries; tolerate it if something does.
      http.get("/api/v1/titles", () => HttpResponse.json({ titles: [] })),
      profilesHandler,
    );

    renderPage();

    await screen.findByText("Alpha Adventure");
    await userEvent.click(screen.getByRole("button", { name: /^add$/i }));
    await userEvent.click(
      await screen.findByRole("button", { name: "Add Alpha Adventure" }),
    );

    expect(
      await screen.findByRole("link", { name: /in library/i }),
    ).toHaveAttribute("href", "/titles/9");
  });

  it("shows the detail as a drawer on mobile", async () => {
    const wide = window.innerWidth;
    window.innerWidth = 375;
    try {
      server.use(
        chartHandler([
          entry({
            provider_id: 101,
            romaji: "Alpha Adventure",
            description: "A compact tale.",
          }),
        ]),
      );

      renderPage();

      await userEvent.click(
        await screen.findByRole("button", { name: /alpha adventure/i }),
      );
      const detail = await screen.findByRole("dialog");
      expect(detail).toHaveAttribute("data-slot", "drawer-content");
      expect(within(detail).getByText("A compact tale.")).toBeInTheDocument();
    } finally {
      window.innerWidth = wide;
    }
  });

  it("refetches the chart for a year picked from the year menu", async () => {
    const requested: (string | null)[] = [];
    server.use(
      http.get("/api/v1/browse/season", ({ request }) => {
        const url = new URL(request.url);
        requested.push(url.searchParams.get("year"));
        return HttpResponse.json({
          season: url.searchParams.get("season"),
          year: Number(url.searchParams.get("year")),
          entries: [entry({ provider_id: 101, romaji: "Alpha Adventure" })],
        });
      }),
    );

    renderPage();

    await screen.findByText("Alpha Adventure");
    const today = currentSeason();
    const year = screen.getByRole("combobox", { name: "Year" });
    expect(year).toHaveTextContent(String(today.year));

    await userEvent.click(year);
    await userEvent.click(await screen.findByRole("option", { name: "2024" }));

    await waitFor(() => expect(requested).toContain("2024"));
    expect(screen.getByRole("combobox", { name: "Year" })).toHaveTextContent(
      "2024",
    );
    expect(
      screen.getByText(seasonLabel({ season: today.season, year: 2024 })),
    ).toBeInTheDocument();
  });

  // A film's premiere is now its next_airs_at, and the card said "Ep 1 in 4h" —
  // an episode line, with a clock, for the noon-UTC placeholder.
  it("dates a film's premiere instead of calling it episode 1", async () => {
    server.use(
      chartHandler([
        entry({
          provider_id: 104,
          romaji: "Placeholder Legend",
          format: "MOVIE",
          next_episode: 1,
          next_airs_at: "2099-03-15T12:00:00Z",
        }),
      ]),
      profilesHandler,
      settingsHandler,
    );

    renderPage();

    expect(await screen.findByText("Placeholder Legend")).toBeInTheDocument();
    expect(screen.getByText(/^Premieres /)).toBeInTheDocument();
    expect(screen.queryByText(/Ep 1/)).not.toBeInTheDocument();
    expect(screen.queryByText(/\bin \d+[mhd]\b/)).not.toBeInTheDocument();
  });

  it("opens the add form for a movie", async () => {
    server.use(
      chartHandler([
        entry({ provider_id: 103, romaji: "Gamma the Movie", format: "MOVIE" }),
      ]),
      profilesHandler,
      settingsHandler,
    );

    const user = userEvent.setup();
    renderPage();

    expect(await screen.findByText("Gamma the Movie")).toBeInTheDocument();
    const add = screen.getByRole("button", { name: /add/i });
    expect(add).toBeEnabled();

    await user.click(add);

    expect(
      await screen.findByRole("button", { name: "Add Gamma the Movie" }),
    ).toBeInTheDocument();
  });
});
