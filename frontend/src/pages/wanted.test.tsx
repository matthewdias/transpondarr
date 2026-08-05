import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter } from "react-router";
import { afterAll, afterEach, beforeAll, expect, it } from "vitest";
import type { CutoffItem, MissingItem } from "@/lib/api";
import { searchQueuedToast } from "@/lib/search-queued-toast";
import { SidebarProvider } from "@/components/ui/sidebar";
import { WantedPage } from "@/pages/wanted";

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const missing = (over: Partial<MissingItem>): MissingItem => ({
  id: 1,
  series_id: 7,
  series_title: "Signal Anomaly",
  monitored: true,
  number: 4,
  reason: "search_due",
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

type MissingPage = { items: MissingItem[]; next_cursor?: string };

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
      return HttpResponse.json(opts.pages?.[cursor] ?? { items: [] });
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

// The reason is the page's whole point over a plain list, and per-row Search
// routes into the episode-targeted Releases tab rather than the series dump.
it("renders each row's reason and links Search at the episode", async () => {
  useHandlers({
    pages: {
      "": {
        items: [
          missing({ id: 1, number: 4, reason: "never_searched" }),
          missing({
            id: 2,
            number: 5,
            reason: "grab_failed",
            reason_detail: "torrent vanished from the client",
          }),
          missing({
            id: 3,
            number: 6,
            reason: "blocklisted",
            blocked_releases: 2,
          }),
        ],
      },
    },
  });
  renderPage();

  expect(await screen.findByText("Not searched yet")).toBeInTheDocument();
  expect(screen.getByText("Last grab failed")).toBeInTheDocument();
  expect(screen.getByText("Releases blocklisted")).toBeInTheDocument();
  expect(
    screen.getByTitle("torrent vanished from the client"),
  ).toBeInTheDocument();
  expect(screen.getByTitle("2 releases")).toBeInTheDocument();

  const links = screen
    .getAllByRole("link", { name: /search/i })
    .map((a) => a.getAttribute("href"));
  expect(links).toEqual([
    "/series/7?item=4",
    "/series/7?item=5",
    "/series/7?item=6",
  ]);
});

// Both toggles reach the server rather than filtering what already arrived: the
// listing is paginated, so a client-side filter would leave short pages.
it("sends the unaired and unmonitored toggles to the server", async () => {
  const seen: URLSearchParams[] = [];
  useHandlers({ pages: { "": { items: [] } }, onMissing: (q) => seen.push(q) });
  renderPage();

  await waitFor(() => expect(seen).toHaveLength(1));
  expect(seen[0].get("unaired")).toBe("false");
  expect(seen[0].get("unmonitored")).toBe("false");

  await userEvent.click(screen.getByLabelText("Show unaired episodes"));
  await waitFor(() => expect(seen).toHaveLength(2));
  expect(seen[1].get("unaired")).toBe("true");
});

it("pages with the cursor the previous page returned", async () => {
  useHandlers({
    pages: {
      "": { items: [missing({ id: 1, number: 1 })], next_cursor: "abc" },
      abc: { items: [missing({ id: 2, number: 2 })] },
    },
  });
  renderPage();

  expect(await screen.findByText("01")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: /load more/i }));
  expect(await screen.findByText("02")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /load more/i })).toBeNull();
});

// Search is a queue, not N indexer requests, and selection is per series even
// though the checkbox is per row.
it("queues a search for the selected rows' series, deduplicated", async () => {
  const bodies: { series_ids?: number[] }[] = [];
  useHandlers({
    pages: {
      "": {
        items: [
          missing({ id: 1, series_id: 7, number: 4 }),
          missing({ id: 2, series_id: 7, number: 5 }),
          missing({
            id: 3,
            series_id: 9,
            series_title: "Other Show",
            number: 1,
          }),
        ],
      },
    },
    onSearch: (body) => bodies.push(body),
  });
  renderPage();

  const selected = await screen.findByLabelText(
    "Select Signal Anomaly episode 4",
  );
  await userEvent.click(selected);
  await userEvent.click(
    screen.getByLabelText("Select Signal Anomaly episode 5"),
  );
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
  useHandlers({ pages: { "": { items: [] } }, cutoffItems: [cutoff({})] });
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
