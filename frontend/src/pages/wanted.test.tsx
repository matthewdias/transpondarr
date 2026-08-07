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
  monitored: true,
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
  monitored: true,
  status: "in_library",
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
  cutoffPages?: Record<string, { groups: CutoffGroup[]; next_cursor?: string }>;
  onSearch?: (body: { series_ids?: number[] }) => void;
  onMissing?: (query: URLSearchParams) => void;
  onSetMonitored?: (body: { item_ids: number[]; monitored: boolean }) => void;
}) {
  server.use(
    http.get("/api/v1/wanted/missing", ({ request }) => {
      const query = new URL(request.url).searchParams;
      opts.onMissing?.(query);
      const cursor = query.get("cursor") ?? "";
      return HttpResponse.json(opts.pages?.[cursor] ?? { groups: [] });
    }),
    http.get("/api/v1/wanted/cutoff-unmet", ({ request }) => {
      const cursor = new URL(request.url).searchParams.get("cursor") ?? "";
      if (opts.cutoffPages) {
        return HttpResponse.json(opts.cutoffPages[cursor] ?? { groups: [] });
      }
      return HttpResponse.json({ groups: opts.cutoffGroups ?? [] });
    }),
    http.patch("/api/v1/wanted/items", async ({ request }) => {
      const body = (await request.json()) as {
        item_ids: number[];
        monitored: boolean;
      };
      opts.onSetMonitored?.(body);
      return HttpResponse.json({
        updated: body.item_ids.length,
        series_queued: body.monitored ? 1 : 0,
      });
    }),
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

// #181's tier. It is the one stored reason on the page, so the chip carries a
// visible age -- a past-tense verb plus "2h ago" cannot read as "now" -- and
// the tooltip names the release, the refusal reason and which entry point
// decided. A row with nothing of its own still stays quiet.
it("dates what the last pass decided and names the release", async () => {
  const twoHoursAgo = new Date(Date.now() - 2 * 60 * 60 * 1000)
    .toISOString()
    .replace("T", " ")
    .slice(0, 19);
  useHandlers({
    pages: {
      "": {
        groups: [
          group({}, [
            missing({ id: 1, number: 4 }),
            missing({
              id: 2,
              number: 5,
              reason: "declined",
              reason_detail: "below the profile floor",
              last_pass: {
                release_title: "[SynthSubs] Signal Anomaly - 05 [720p]",
                source: "sweep",
                at: twoHoursAgo,
              },
            }),
            missing({
              id: 3,
              number: 6,
              reason: "no_match",
              last_pass: { source: "sweep", at: twoHoursAgo },
            }),
          ]),
        ],
      },
    },
  });
  renderPage();

  expect(await screen.findByText("Releases declined · 2h ago")).toBeVisible();
  expect(screen.getByText("Nothing matched · 2h ago")).toBeVisible();
  const declined = screen.getByText("Releases declined · 2h ago");
  expect(declined.getAttribute("title")).toContain(
    "[SynthSubs] Signal Anomaly - 05 [720p]",
  );
  expect(declined.getAttribute("title")).toContain("below the profile floor");
  expect(declined.getAttribute("title")).toContain("search");
  // Episode 4 has no story of its own; its group carries it.
  expect(screen.queryByText(/Releases declined · 2h ago/)).toBe(declined);
  expect(screen.getByText("Episode 4")).toBeInTheDocument();
});

// A hold names what it is waiting for and how long is left, which is how the
// pin delay gets validated empirically (#62). An add failure shares the failed
// grab's destructive tone: the client refused it, which is not the profile
// turning a release down.
it("names a pinned-group wait and tones a refused add as a failure", async () => {
  const justNow = new Date(Date.now() - 60 * 1000)
    .toISOString()
    .replace("T", " ")
    .slice(0, 19);
  const inFourHours = new Date(Date.now() + 4 * 60 * 60 * 1000)
    .toISOString()
    .replace("T", " ")
    .slice(0, 19);
  useHandlers({
    pages: {
      "": {
        groups: [
          group({}, [
            missing({
              id: 1,
              number: 1,
              reason: "pin_held",
              reason_detail: 'waiting for the pinned group "PinnedSubs"',
              last_pass: {
                release_title: "[OtherSubs] Signal Anomaly - 01 [1080p]",
                source: "feed",
                at: justNow,
                held_until: inFourHours,
              },
            }),
            missing({
              id: 2,
              number: 2,
              reason: "add_failed",
              reason_detail: "404 fetching .torrent",
              last_pass: {
                release_title: "[SynthSubs] Signal Anomaly - 02 [1080p]",
                source: "sweep",
                at: justNow,
              },
            }),
          ]),
        ],
      },
    },
  });
  renderPage();

  const held = await screen.findByText(/Waiting for the pinned group/);
  expect(held.getAttribute("title")).toContain("PinnedSubs");
  expect(held.getAttribute("title")).toMatch(/Grabbable in \d+h/);
  expect(held.getAttribute("title")).toContain("feed");

  const refused = screen.getByText(/Download client refused it/);
  expect(refused.className).toContain("destructive");
  expect(refused.getAttribute("title")).toContain("404 fetching .torrent");
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

// Cutoff Unmet decides membership in Go, so a request can scan its whole budget
// without finding anything and still have somewhere to resume. Saying "nothing
// below cutoff" there would hide a library that has some, further down.
it("offers to keep looking when an empty cutoff page still has a cursor", async () => {
  useHandlers({
    pages: { "": { groups: [] } },
    cutoffPages: {
      "": { groups: [], next_cursor: "abc" },
      abc: { groups: [cutoffGroup({}, [cutoff({})])] },
    },
  });
  renderPage();

  await userEvent.click(screen.getByRole("tab", { name: /cutoff unmet/i }));
  expect(await screen.findByText("None found yet")).toBeInTheDocument();
  expect(screen.queryByText("Nothing below cutoff")).toBeNull();

  await userEvent.click(screen.getByRole("button", { name: /keep looking/i }));
  expect(
    await screen.findByText("[FakeGroup] Signal Anomaly - 02 [720p]"),
  ).toBeInTheDocument();
});

// The last page with nothing on it is the real empty state.
it("says nothing below cutoff when the scan is exhausted", async () => {
  useHandlers({
    pages: { "": { groups: [] } },
    cutoffPages: { "": { groups: [] } },
  });
  renderPage();

  await userEvent.click(screen.getByRole("tab", { name: /cutoff unmet/i }));
  expect(await screen.findByText("Nothing below cutoff")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /keep looking/i })).toBeNull();
});

// A held release can meet every preference its profile states and still sit
// below the cutoff. The row says so rather than rendering an empty space that
// reads as a bug -- as two facts, not a verdict: unmet goals exclude the
// repack/v2 bonus, so an empty list is not proof that nothing can clear the
// cutoff, and the copy must not claim it is.
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
    "This release meets every preference this profile states. It stays listed because its score (2400) is below the cutoff (2500).",
  );
  expect(note.getAttribute("title")).not.toMatch(/best possible|nothing can/i);
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
  // The accessible name follows the state rather than contradicting it.
  await userEvent.click(
    screen.getByRole("button", { name: "Expand Signal Anomaly" }),
  );
  expect(screen.getByText("Episode 4")).toBeInTheDocument();
});

// A selection the user can no longer see must not still be queued: changing
// scope reloads the list, so it drops the selection with it.
it("clears the selection when the scope filters change", async () => {
  const bodies: { series_ids?: number[] }[] = [];
  useHandlers({
    pages: {
      "": { groups: [group({}, [missing({ id: 1, number: 4 })])] },
    },
    onSearch: (body) => bodies.push(body),
  });
  renderPage();

  await userEvent.click(await screen.findByLabelText("Select Signal Anomaly"));
  expect(
    screen.getByRole("button", { name: /search selected \(1\)/i }),
  ).toBeEnabled();

  await userEvent.click(screen.getByRole("button", { name: "Unaired" }));
  const button = await screen.findByRole("button", {
    name: /search selected$/i,
  });
  expect(button).toBeDisabled();
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

// #188: an unmonitored row is only reachable behind the toggle, so it has to
// say why it is quiet -- and the pass tier it may still carry is suppressed,
// since nothing will ever revisit it.
it("labels an unmonitored row and offers to re-monitor it", async () => {
  const calls: { item_ids: number[]; monitored: boolean }[] = [];
  useHandlers({
    pages: {
      "": {
        groups: [
          group({}, [
            missing({
              id: 9,
              number: 4,
              monitored: false,
              reason: "unmonitored",
            }),
          ]),
        ],
      },
    },
    onSetMonitored: (body) => calls.push(body),
  });
  renderPage();

  expect(await screen.findByText("Not monitored")).toBeInTheDocument();

  const user = userEvent.setup();
  await user.click(
    screen.getByRole("button", { name: /^monitor episode 4$/i }),
  );
  await waitFor(() =>
    expect(calls).toEqual([{ item_ids: [9], monitored: true }]),
  );
});

// The click that hides a row is one request, from the row itself.
it("unmonitors a missing row in place", async () => {
  const calls: { item_ids: number[]; monitored: boolean }[] = [];
  useHandlers({
    pages: { "": { groups: [group({}, [missing({ id: 3, number: 6 })])] } },
    onSetMonitored: (body) => calls.push(body),
  });
  renderPage();

  const user = userEvent.setup();
  await user.click(
    await screen.findByRole("button", { name: /stop monitoring episode 6/i }),
  );
  await waitFor(() =>
    expect(calls).toEqual([{ item_ids: [3], monitored: false }]),
  );
});
