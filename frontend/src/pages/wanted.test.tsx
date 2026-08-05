import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter } from "react-router";
import { afterAll, afterEach, beforeAll, expect, it } from "vitest";
import type { CutoffItem, MissingGroup, MissingItem } from "@/lib/api";
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
  series_id: 7,
  series_title: "Signal Anomaly",
  monitored: true,
  number: 2,
  status: "have",
  held_release: "[FakeGroup] Signal Anomaly - 02 [720p]",
  score: 2100,
  cutoff_score: 2300,
  profile_name: "Anime HD",
  ...over,
});

type MissingPage = {
  global_reason?: string;
  groups: MissingGroup[];
  next_cursor?: string;
};

function useHandlers(opts: {
  pages?: Record<string, MissingPage>;
  cutoffItems?: CutoffItem[];
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
      HttpResponse.json({ items: opts.cutoffItems ?? [] }),
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
  const more = screen.getByRole("link", { name: /58 more episodes/i });
  expect(more).toHaveAttribute("href", "/series/7");
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

it("shows a held release against its profile cutoff on the second tab", async () => {
  useHandlers({ pages: { "": { groups: [] } }, cutoffItems: [cutoff({})] });
  renderPage();

  await userEvent.click(screen.getByRole("tab", { name: /cutoff unmet/i }));
  const row = await screen.findByText("[FakeGroup] Signal Anomaly - 02 [720p]");
  expect(row).toBeInTheDocument();
  expect(screen.getByText("2100 / 2300")).toBeInTheDocument();
  expect(
    screen.getByTitle("Scored under the Anime HD profile"),
  ).toBeInTheDocument();
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
