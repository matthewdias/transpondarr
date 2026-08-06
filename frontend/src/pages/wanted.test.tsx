import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter } from "react-router";
import { afterAll, afterEach, beforeAll, expect, it } from "vitest";
import type {
  CutoffGroup,
  CutoffItem,
  MissingGroup,
  MissingItem,
} from "@/lib/api";
import { searchQueuedToast } from "@/lib/search-queued-toast";
import { SidebarProvider } from "@/components/ui/sidebar";
import { WantedPage } from "@/pages/wanted";

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const missing = (over: Partial<MissingItem>): MissingItem => ({
  id: 1,
  number: 4,
  ...over,
});

const group = (
  over: Partial<MissingGroup>,
  items: MissingItem[],
): MissingGroup => ({
  series_id: 7,
  series_title: "Signal Anomaly",
  monitored: true,
  reason: "search_due",
  missing: items.length,
  items,
  ...over,
});

const cutoff = (over: Partial<CutoffItem>): CutoffItem => ({
  id: 11,
  number: 2,
  status: "have",
  held_release: "[FakeGroup] Signal Anomaly - 02 [720p]",
  score: 2100,
  ...over,
});

const cutoffGroup = (
  over: Partial<CutoffGroup>,
  items: CutoffItem[],
): CutoffGroup => ({
  series_id: 7,
  series_title: "Signal Anomaly",
  monitored: true,
  profile_name: "Anime HD",
  cutoff_score: 2300,
  below: items.length,
  items,
  ...over,
});

type MissingPage = {
  global_reason?: string;
  groups: MissingGroup[];
  next_cursor?: string;
};

function useHandlers(opts: {
  pages?: Record<string, MissingPage>;
  cutoffGroups?: CutoffGroup[];
  onSearch?: (body: { series_ids?: number[] }) => void;
  onMissing?: (query: URLSearchParams) => void;
}) {
  server.use(
    http.get("/api/v1/wanted/missing", ({ request }) => {
      const query = new URL(request.url).searchParams;
      opts.onMissing?.(query);
      const cursor = query.get("cursor") ?? "";
      return HttpResponse.json(opts.pages?.[cursor] ?? { groups: [] });
    }),
    http.get("/api/v1/wanted/cutoff-unmet", () =>
      HttpResponse.json({ groups: opts.cutoffGroups ?? [] }),
    ),
    http.post("/api/v1/wanted/search", async ({ request }) => {
      const body = (await request.json()) as { series_ids?: number[] };
      opts.onSearch?.(body);
      return HttpResponse.json(
        {
          series_queued: body.series_ids?.length ? body.series_ids.length : -1,
          automation: "on",
          run_triggered: true,
        },
        { status: 202 },
      );
    }),
  );
}

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <SidebarProvider>
          <WantedPage />
        </SidebarProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return client;
}

// The three reason tiers render together: the series' story on its group
// header, an item's own story on its row, and rows with nothing to add stay
// quiet. Per-row Search routes into the episode-targeted Releases tab.
it("renders group and item reasons on their own tiers", async () => {
  useHandlers({
    pages: {
      "": {
        groups: [
          group({ reason: "blocklisted", blocked_releases: 2 }, [
            missing({ id: 1, number: 4 }),
            missing({
              id: 2,
              number: 5,
              reason: "grab_failed",
              reason_detail: "torrent vanished from the client",
            }),
          ]),
        ],
      },
    },
  });
  renderPage();

  expect(await screen.findByText("Releases blocklisted")).toBeInTheDocument();
  expect(screen.getByTitle("2 releases")).toBeInTheDocument();
  expect(screen.getByText("Last grab failed")).toBeInTheDocument();
  expect(
    screen.getByTitle("torrent vanished from the client"),
  ).toBeInTheDocument();
  expect(screen.getByText("2 episodes missing")).toBeInTheDocument();
  // An absent broadcast time is named, not left as a dash that could mean
  // anything from "loading" to "unknown column".
  expect(screen.getAllByText("No air date")).toHaveLength(2);

  const links = screen
    .getAllByRole("link", { name: /search/i })
    .map((a) => a.getAttribute("href"));
  expect(links).toEqual(["/series/7?item=4", "/series/7?item=5"]);
});

// The page tier is a banner said once, not a badge stamped on every row.
it("shows the global reason as one banner", async () => {
  useHandlers({
    pages: {
      "": {
        global_reason: "notify_only",
        groups: [group({}, [missing({ id: 1, number: 1 })])],
      },
    },
  });
  renderPage();

  expect(
    await screen.findByText(/automation is rehearsing/i),
  ).toBeInTheDocument();
});

// A capped group still states its full size and offers the series page.
it("links to the series for episodes past the group cap", async () => {
  useHandlers({
    pages: {
      "": {
        groups: [
          group({ missing: 60 }, [
            missing({ id: 1, number: 1 }),
            missing({ id: 2, number: 2 }),
          ]),
        ],
      },
    },
  });
  renderPage();

  expect(await screen.findByText("60 episodes missing")).toBeInTheDocument();
  const more = screen.getByRole("link", { name: /go to series/i });
  expect(more).toHaveAttribute("href", "/series/7");
  expect(more).toHaveTextContent("58 more episodes not shown");
});

// The scope filters are one group, not two loose buttons: arrows move between
// chips and the keyboard toggles them. Asserted through the request the toggle
// causes, so it fails if the group is focusable but not operable.
it("moves between the scope chips with arrows and toggles from the keyboard", async () => {
  const seen: URLSearchParams[] = [];
  useHandlers({
    pages: { "": { groups: [] } },
    onMissing: (q) => seen.push(q),
  });
  renderPage();

  await waitFor(() => expect(seen).toHaveLength(1));
  screen.getByRole("button", { name: "Unaired" }).focus();
  await userEvent.keyboard("{ArrowRight}");
  expect(screen.getByRole("button", { name: "Unmonitored" })).toHaveFocus();

  await userEvent.keyboard("{Enter}");
  await waitFor(() => expect(seen).toHaveLength(2));
  expect(seen[1].get("unmonitored")).toBe("true");
  expect(seen[1].get("unaired")).toBe("false");
});

// Both toggles reach the server rather than filtering what already arrived: the
// listing is paginated, so a client-side filter would leave short pages.
it("sends the unaired and unmonitored toggles to the server", async () => {
  const seen: URLSearchParams[] = [];
  useHandlers({
    pages: { "": { groups: [] } },
    onMissing: (q) => seen.push(q),
  });
  renderPage();

  await waitFor(() => expect(seen).toHaveLength(1));
  expect(seen[0].get("unaired")).toBe("false");
  expect(seen[0].get("unmonitored")).toBe("false");

  await userEvent.click(screen.getByRole("button", { name: "Unaired" }));
  await waitFor(() => expect(seen).toHaveLength(2));
  expect(seen[1].get("unaired")).toBe("true");
});

it("pages whole groups with the cursor the previous page returned", async () => {
  useHandlers({
    pages: {
      "": {
        groups: [group({}, [missing({ id: 1, number: 1 })])],
        next_cursor: "abc",
      },
      abc: {
        groups: [
          group({ series_id: 9, series_title: "Other Show" }, [
            missing({ id: 2, number: 2 }),
          ]),
        ],
      },
    },
  });
  renderPage();

  expect(await screen.findByText("Signal Anomaly")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: /load more/i }));
  expect(await screen.findByText("Other Show")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /load more/i })).toBeNull();
});

// Selection is per group because a search is per series: the sweep's unit.
it("queues a search for the selected groups' series", async () => {
  const bodies: { series_ids?: number[] }[] = [];
  useHandlers({
    pages: {
      "": {
        groups: [
          group({}, [
            missing({ id: 1, number: 4 }),
            missing({ id: 2, number: 5 }),
          ]),
          group({ series_id: 9, series_title: "Other Show" }, [
            missing({ id: 3, number: 1 }),
          ]),
        ],
      },
    },
    onSearch: (body) => bodies.push(body),
  });
  renderPage();

  await userEvent.click(await screen.findByLabelText("Select Signal Anomaly"));
  await userEvent.click(
    screen.getByRole("button", { name: /search selected/i }),
  );

  await waitFor(() => expect(bodies).toHaveLength(1));
  expect(bodies[0].series_ids).toEqual([7]);

  await userEvent.click(screen.getByRole("button", { name: /search all/i }));
  await waitFor(() => expect(bodies).toHaveLength(2));
  expect(bodies[1].series_ids).toEqual([]);
});

// Goals shared by every item hoist to the group header; a row keeps only what
// is its own, and the profile and cutoff live on the header outright.
it("hoists shared goals to the cutoff group header", async () => {
  useHandlers({
    pages: { "": { groups: [] } },
    cutoffGroups: [
      cutoffGroup({}, [
        cutoff({
          id: 11,
          number: 2,
          unmet_goals: [
            { label: "group FakeTop", points: 100 },
            { label: "resolution 1080p", points: 100 },
          ],
        }),
        cutoff({
          id: 12,
          number: 3,
          held_release: "[FakeTop] Signal Anomaly - 03 [720p]",
          score: 2200,
          unmet_goals: [{ label: "resolution 1080p", points: 100 }],
        }),
      ]),
    ],
  });
  renderPage();

  await userEvent.click(screen.getByRole("tab", { name: /cutoff unmet/i }));
  expect(
    await screen.findByText("[FakeGroup] Signal Anomaly - 02 [720p]"),
  ).toBeInTheDocument();
  expect(screen.getByText("2 episodes below cutoff")).toBeInTheDocument();
  expect(screen.getByText("Anime HD · cutoff 2300")).toBeInTheDocument();
  // The resolution gap is everyone's, so it is said once on the header...
  expect(
    screen.getByText("Wanted: resolution 1080p (+100)"),
  ).toBeInTheDocument();
  // ...and the group gap stays on the one row that has it.
  expect(
    screen.getByText("Also wants group FakeTop (+100)"),
  ).toBeInTheDocument();
  expect(screen.getByText("2100 / 2300")).toBeInTheDocument();
  expect(screen.getByText("2200 / 2300")).toBeInTheDocument();
});

// A held release can top every axis its profile states and still sit below the
// cutoff, when the cutoff is above what the profile can score. The row says so
// rather than rendering an empty space that reads as a bug.
it("says when a sub-cutoff row has nothing left to improve", async () => {
  useHandlers({
    pages: { "": { groups: [] } },
    cutoffGroups: [
      cutoffGroup({ cutoff_score: 2500 }, [
        cutoff({ id: 11, number: 1, score: 2400 }),
      ]),
    ],
  });
  renderPage();

  await userEvent.click(screen.getByRole("tab", { name: /cutoff unmet/i }));
  const note = await screen.findByText("Nothing left to improve");
  expect(note).toHaveAttribute(
    "title",
    "This profile's best possible score is 2400, below its cutoff of 2500, so nothing can clear it",
  );
  // The header stays quiet: an item below the ceiling on the same profile is
  // healthy, so the group is not the place to say this.
  expect(screen.queryByText(/^Wanted:/)).toBeNull();
});

// Both tabs' groups collapse from the header, keeping the header's summary.
it("collapses a group to its header", async () => {
  useHandlers({
    pages: {
      "": { groups: [group({}, [missing({ id: 1, number: 4 })])] },
    },
  });
  renderPage();

  expect(await screen.findByText("Episode 4")).toBeInTheDocument();
  await userEvent.click(
    screen.getByRole("button", { name: "Collapse Signal Anomaly" }),
  );
  expect(screen.queryByText("Episode 4")).toBeNull();
  expect(screen.getByText("1 episode missing")).toBeInTheDocument();
  await userEvent.click(
    screen.getByRole("button", { name: "Collapse Signal Anomaly" }),
  );
  expect(screen.getByText("Episode 4")).toBeInTheDocument();
});

// The endpoint queues; saying it searched would be wrong, and under notify-only
// saying it grabbed would be worse.
it("words the queued-search toast for what actually happened", () => {
  expect(
    searchQueuedToast({
      series_queued: -1,
      automation: "on",
      run_triggered: true,
    }).title,
  ).toBe("Search queued for every series.");
  expect(
    searchQueuedToast({
      series_queued: 3,
      automation: "notify_only",
      run_triggered: true,
    }).title,
  ).toContain("nothing will be grabbed");
  expect(
    searchQueuedToast({
      series_queued: 1,
      automation: "on",
      run_triggered: false,
    }),
  ).toMatchObject({
    title: "Search queued for 1 series.",
    description: "The next scheduled sweep will pick it up.",
  });
});
