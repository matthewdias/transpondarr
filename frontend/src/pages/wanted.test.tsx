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
  title_id: 7,
  title: "Signal Anomaly",
  format: "TV",
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
  title_id: 7,
  title: "Signal Anomaly",
  format: "TV",
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
  onSearch?: (body: { title_ids?: number[] }) => void;
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
        titles_queued: body.monitored ? 1 : 0,
      });
    }),
    http.post("/api/v1/wanted/search", async ({ request }) => {
      const body = (await request.json()) as { title_ids?: number[] };
      opts.onSearch?.(body);
      return HttpResponse.json(
        {
          titles_queued: body.title_ids?.length ? body.title_ids.length : -1,
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
  expect(links).toEqual([
    "/titles/7?tab=releases&item=4",
    "/titles/7?tab=releases&item=5",
  ]);
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
  const more = screen.getByRole("link", { name: /go to title/i });
  expect(more).toHaveAttribute("href", "/titles/7");
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
          group({ title_id: 9, title: "Other Show" }, [
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
  const bodies: { title_ids?: number[] }[] = [];
  useHandlers({
    pages: {
      "": {
        groups: [
          group({}, [
            missing({ id: 1, number: 4 }),
            missing({ id: 2, number: 5 }),
          ]),
          group({ title_id: 9, title: "Other Show" }, [
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
  expect(bodies[0].title_ids).toEqual([7]);

  await userEvent.click(screen.getByRole("button", { name: /search all/i }));
  await waitFor(() => expect(bodies).toHaveLength(2));
  expect(bodies[1].title_ids).toEqual([]);
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
  const bodies: { title_ids?: number[] }[] = [];
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
      titles_queued: -1,
      automation: "on",
      run_triggered: true,
    }).title,
  ).toBe("Search queued for every title.");
  expect(
    searchQueuedToast({
      titles_queued: 3,
      automation: "notify_only",
      run_triggered: true,
    }).title,
  ).toContain("nothing will be grabbed");
  expect(
    searchQueuedToast({
      titles_queued: 1,
      automation: "on",
      run_triggered: false,
    }),
  ).toMatchObject({
    title: "Search queued for 1 title.",
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

// The count follows the filter, so with the Unmonitored chip on a narrowed
// long-runner reads "1173 episodes missing" above rows deliberately switched
// off. Splitting it says what is actually being chased.
it("splits the group count when unmonitored rows are shown", async () => {
  useHandlers({
    pages: {
      "": {
        groups: [
          group({ missing: 3 }, [
            missing({ id: 1, number: 1 }),
            missing({ id: 2, number: 2, monitored: false }),
            missing({ id: 3, number: 3, monitored: false }),
          ]),
        ],
      },
    },
  });
  renderPage();

  expect(
    await screen.findByText("1 missing · 2 not monitored"),
  ).toBeInTheDocument();
});

// With items paged the loaded rows are a sample, so a breakdown derived from
// them would understate what is off -- a worse lie than the plain count.
it("keeps the plain count when the group is truncated", async () => {
  useHandlers({
    pages: {
      "": {
        groups: [
          group({ missing: 90 }, [
            missing({ id: 1, number: 1 }),
            missing({ id: 2, number: 2, monitored: false }),
          ]),
        ],
      },
    },
  });
  renderPage();

  expect(await screen.findByText("90 episodes missing")).toBeInTheDocument();
});

// Every Cutoff Unmet row is in the library by definition, so the wanted
// substitution can never fire there and the qualifier is its only marking.
it("offers an unmonitored cutoff row its own monitor toggle", async () => {
  useHandlers({
    cutoffGroups: [
      cutoffGroup({}, [
        cutoff({ id: 11, number: 2 }),
        cutoff({ id: 12, number: 3, monitored: false }),
      ]),
    ],
  });
  renderPage();

  const user = userEvent.setup();
  await user.click(screen.getByRole("tab", { name: /cutoff unmet/i }));

  // The library really does hold both, so both keep that status; the toggle is
  // what distinguishes them, and re-monitoring stays reachable from this tab.
  expect(await screen.findAllByText("In library")).toHaveLength(2);
  expect(
    screen.getByRole("button", { name: /^monitor episode 3$/i }),
  ).toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: /stop monitoring episode 2/i }),
  ).toBeInTheDocument();
});

// #215's DTO half. A film reaches this page like anything else, and format is
// the only thing that can tell it apart -- item count cannot (#208), because a
// one-episode OVA is a series. Air date far enough out that the countdown
// branch cannot fire for either row: what is under test is the wording.
it("never calls a film's item an episode, or dates it to the hour", async () => {
  const airsAt = new Date(Date.now() + 40 * 86400 * 1000).toISOString();
  useHandlers({
    pages: {
      "": {
        groups: [
          group({ title: "Placeholder Film", format: "MOVIE" }, [
            missing({ id: 1, number: 1, airs_at: airsAt, reason: "unaired" }),
          ]),
        ],
      },
    },
  });
  renderPage();

  expect(await screen.findByText("Placeholder Film")).toBeInTheDocument();
  expect(screen.getByText("Film")).toBeInTheDocument();
  expect(screen.queryByText(/episode 1/i)).not.toBeInTheDocument();
  // The group's count line is episodic too, and a film's is always 1. Scoped
  // to the count span, since the tab beside it is also called Missing.
  expect(
    screen.getByText("Missing", { selector: "span.tabular-nums" }),
  ).toBeInTheDocument();
  expect(screen.queryByText(/1 episode missing/i)).not.toBeInTheDocument();
  // The badge is right; only the word was wrong.
  expect(screen.getByText("Not released yet")).toBeInTheDocument();
  expect(screen.queryByText("Not aired yet")).not.toBeInTheDocument();
});

// A film's stored instant may be noon UTC standing in for a day (#224), so a
// countdown would state precision the provider never published. Inside the
// week, which is exactly where countdownOrDate would otherwise count down.
it("shows a film's near release as a date, never as a countdown", async () => {
  const airsAt = new Date(Date.now() + 3 * 86400 * 1000).toISOString();
  useHandlers({
    pages: {
      "": {
        groups: [
          group({ title_id: 7, title: "Placeholder Film", format: "MOVIE" }, [
            missing({ id: 1, number: 1, airs_at: airsAt }),
          ]),
          group({ title_id: 8, title: "Placeholder Saga" }, [
            missing({ id: 2, number: 4, airs_at: airsAt }),
          ]),
        ],
      },
    },
  });
  renderPage();

  await screen.findByText("Placeholder Film");
  // The episode beside it still counts down, so this is the format branch and
  // not a page-wide change of mind.
  expect(screen.getByText(/^in \d+d$/)).toBeInTheDocument();
  expect(screen.getAllByText(/^in \d+d$/)).toHaveLength(1);
});

// The cutoff tab groups by title too, and its count line is just as episodic.
it("keeps a film's cutoff group from counting in episodes", async () => {
  useHandlers({
    cutoffGroups: [
      cutoffGroup({ title: "Placeholder Film", format: "MOVIE" }, [
        cutoff({ id: 11, number: 1 }),
      ]),
    ],
  });
  renderPage();

  const user = userEvent.setup();
  await user.click(screen.getByRole("tab", { name: /cutoff unmet/i }));

  expect(await screen.findByText("Below cutoff")).toBeInTheDocument();
  expect(screen.queryByText(/1 episode below cutoff/i)).not.toBeInTheDocument();
  // #231: the upgrade row emits the same link as the missing one, so it takes
  // the same film branch.
  expect(screen.getByRole("link", { name: /search/i })).toHaveAttribute(
    "href",
    "/titles/7?tab=releases",
  );
});

// #231 split the two parameters: ?tab picks the tab and ?item focuses an item,
// so a row states only what is true of it. A film's item is not a choice, and
// format is the only thing that can say so -- a one-item OVA still numbers its
// episode, which is why the OVA row here carries ?item and the film's does not.
it("asks for a focused item only where there is a choice of item", async () => {
  useHandlers({
    pages: {
      "": {
        groups: [
          group({ title_id: 7, title: "Placeholder Film", format: "MOVIE" }, [
            missing({ id: 1, number: 1 }),
          ]),
          group({ title_id: 8, title: "Placeholder OVA", format: "OVA" }, [
            missing({ id: 2, number: 1 }),
          ]),
          group({ title_id: 9, title: "Placeholder Saga" }, [
            missing({ id: 3, number: 4 }),
          ]),
        ],
      },
    },
  });
  renderPage();

  await screen.findByText("Placeholder Film");
  const links = screen
    .getAllByRole("link", { name: /search/i })
    .map((a) => a.getAttribute("href"));
  expect(links).toEqual([
    "/titles/7?tab=releases",
    "/titles/8?tab=releases&item=1",
    "/titles/9?tab=releases&item=4",
  ]);
});
