import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";
import { HistoryTab } from "@/components/detail/history-tab";
import { GrabEventRow } from "@/components/grab-event-row";
import type { BlocklistEntry, GrabEvent } from "@/lib/api";

function event(overrides: Partial<GrabEvent>): GrabEvent {
  return {
    id: 1,
    item_number: 3,
    infohash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    release_title: "[FakeGroup] Example Show - 03 (1080p) [ABCD1234].mkv",
    status: "grabbed",
    created_at: new Date().toISOString(),
    ...overrides,
  };
}

describe("GrabEventRow", () => {
  // History is past-tense: a grabbed event is a moment, not live progress.
  it("shows a grabbed event as Grabbed", () => {
    render(<GrabEventRow event={event({ status: "grabbed" })} />);
    expect(screen.getByText(/Grabbed/)).toBeInTheDocument();
    expect(screen.queryByText(/Downloading/)).not.toBeInTheDocument();
  });

  // Regression pin from issue #47: a terminal failure must not read as in-progress.
  it("shows a failed event as Failed, with its detail", () => {
    render(
      <GrabEventRow
        event={event({
          status: "failed",
          detail: "the download vanished from the client",
        })}
      />,
    );
    expect(screen.getByText(/Failed/)).toBeInTheDocument();
    expect(
      screen.getByText("the download vanished from the client"),
    ).toBeInTheDocument();
  });
});

// created_at is emitted raw, in SQLite's bare-UTC form, while blocked_until goes
// through the server's RFC 3339 conversion. The fixture mirrors that asymmetry.
const sqliteTime = (at: number) =>
  new Date(at).toISOString().replace("T", " ").slice(0, 19);

// A minute of slack, so a countdown assertion cannot straddle an hour boundary.
const futureISO = (ms: number) =>
  new Date(Date.now() + ms + 60_000).toISOString();

const blocklistEntry = (
  over: Partial<BlocklistEntry> = {},
): BlocklistEntry => ({
  id: 11,
  release_title: "[FakeGroup] Example Show - 03 (1080p) [ABCD1234].mkv",
  reason: "the download client reported an error",
  failures: 1,
  active: true,
  blocked_until: new Date(Date.now() + 3600_000).toISOString(),
  created_at: sqliteTime(Date.now()),
  ...over,
});

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

function renderTab(
  events: GrabEvent[],
  entries: BlocklistEntry[],
  blocklistFails = false,
) {
  server.use(
    http.get("/api/v1/titles/7/grabs", () =>
      HttpResponse.json({ series: "Example Show", events }),
    ),
    http.get("/api/v1/titles/7/blocklist", () =>
      blocklistFails
        ? new HttpResponse(null, { status: 500 })
        : HttpResponse.json({ series: "Example Show", entries }),
    ),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <HistoryTab seriesId={7} active />
    </QueryClientProvider>,
  );
}

describe("HistoryTab blocked releases", () => {
  // A re-grab overwrites the failed grab row, so the blocked list is often the
  // only remaining trace: an empty feed must not swallow the section.
  it("shows blocked releases even when there is no grab history", async () => {
    renderTab([], [blocklistEntry()]);
    expect(await screen.findByText(/Blocked releases/i)).toBeInTheDocument();
    expect(
      screen.getByText("[FakeGroup] Example Show - 03 (1080p) [ABCD1234].mkv"),
    ).toBeInTheDocument();
    expect(screen.getByText(/No grab or import history/i)).toBeInTheDocument();
  });

  it("says when a release is blocked permanently", async () => {
    renderTab([], [blocklistEntry({ blocked_until: undefined, failures: 3 })]);
    expect(await screen.findByText(/blocked permanently/i)).toBeInTheDocument();
  });

  // Expired entries are history, not enforcement, so they must not pad the list
  // a user reads to see what is currently being skipped.
  it("collapses expired blocks when something is still blocked", async () => {
    renderTab(
      [],
      [
        blocklistEntry({
          id: 1,
          release_title: "[Live] Example Show - 03.mkv",
        }),
        blocklistEntry({
          id: 2,
          release_title: "[Lapsed] Example Show - 02.mkv",
          active: false,
          blocked_until: new Date(Date.now() - 3600_000).toISOString(),
        }),
      ],
    );

    expect(
      await screen.findByText("[Live] Example Show - 03.mkv"),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("[Lapsed] Example Show - 02.mkv"),
    ).not.toBeInTheDocument();

    const toggle = screen.getByRole("button", { name: /1 expired block/i });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    await userEvent.setup().click(toggle);
    expect(
      await screen.findByText("[Lapsed] Example Show - 02.mkv"),
    ).toBeInTheDocument();
  });

  // With nothing else in the section to read, a collapsed disclosure would be a
  // dead end, so the expired list opens on its own.
  it("says when a block has expired rather than showing it as active", async () => {
    renderTab(
      [],
      [
        blocklistEntry({
          active: false,
          failures: 2,
          blocked_until: new Date(Date.now() - 3600_000).toISOString(),
        }),
      ],
    );
    expect(await screen.findByText(/block expired/i)).toBeInTheDocument();
    expect(screen.getByText(/2 failures/)).toBeInTheDocument();
    expect(screen.queryByText(/^unblocks/i)).not.toBeInTheDocument();
  });

  // The near-term expiry is a countdown, so the label has to read as a sentence
  // with it: "Unblocks in 20h", never "Blocked until in 20h".
  it("phrases a live block as a countdown that reads as English", async () => {
    renderTab(
      [],
      [blocklistEntry({ blocked_until: futureISO(20 * 3600_000) })],
    );
    expect(await screen.findByText("Unblocks in 20h")).toBeInTheDocument();
  });

  // A failed blocklist fetch must say so, not silently render an unblocked series.
  it("reports a blocklist that could not be loaded", async () => {
    renderTab([event({})], [], true);
    expect(
      await screen.findByText(/load blocked releases/i),
    ).toBeInTheDocument();
    expect(screen.getByText(/Grabbed/)).toBeInTheDocument();
  });

  it("omits the section when nothing is blocked", async () => {
    renderTab([event({})], []);
    expect(await screen.findByText(/Grabbed/)).toBeInTheDocument();
    expect(screen.queryByText(/Blocked releases/i)).not.toBeInTheDocument();
  });

  it("unblocks a release through the delete endpoint", async () => {
    let deleted = false;
    renderTab([], [blocklistEntry()]);
    server.use(
      http.delete("/api/v1/titles/7/blocklist/11", () => {
        deleted = true;
        return new HttpResponse(null, { status: 204 });
      }),
      http.get("/api/v1/titles/7/blocklist", () =>
        HttpResponse.json({
          series: "Example Show",
          entries: deleted ? [] : [blocklistEntry()],
        }),
      ),
    );
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Unblock" }));
    await waitFor(() => expect(deleted).toBe(true));
    await waitFor(() =>
      expect(screen.queryByText(/Blocked releases/i)).not.toBeInTheDocument(),
    );
  });

  // The affordance a fan-out needs: an environmental fault can block a whole
  // series' candidate pool, and clearing it one entry at a time is the problem.
  it("unblocks the whole series in one request", async () => {
    let cleared = false;
    renderTab([], [blocklistEntry(), blocklistEntry({ id: 12 })]);
    server.use(
      http.delete("/api/v1/titles/7/blocklist", ({ request }) => {
        expect(new URL(request.url).searchParams.get("expired")).toBeNull();
        cleared = true;
        return HttpResponse.json({ cleared: 2 });
      }),
      http.get("/api/v1/titles/7/blocklist", () =>
        HttpResponse.json({
          series: "Example Show",
          entries: cleared
            ? []
            : [blocklistEntry(), blocklistEntry({ id: 12 })],
        }),
      ),
    );
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", { name: /unblock all/i }),
    );
    await waitFor(() => expect(cleared).toBe(true));
    await waitFor(() =>
      expect(screen.queryByText(/Blocked releases/i)).not.toBeInTheDocument(),
    );
  });

  it("forgets expired blocks without touching what still blocks", async () => {
    let expiredOnly = false;
    const live = blocklistEntry();
    const lapsed = blocklistEntry({
      id: 12,
      active: false,
      blocked_until: new Date(Date.now() - 3600_000).toISOString(),
    });
    renderTab([], [live, lapsed]);
    server.use(
      http.delete("/api/v1/titles/7/blocklist", ({ request }) => {
        expiredOnly =
          new URL(request.url).searchParams.get("expired") === "true";
        return HttpResponse.json({ cleared: 1 });
      }),
      http.get("/api/v1/titles/7/blocklist", () =>
        HttpResponse.json({
          series: "Example Show",
          entries: expiredOnly ? [live] : [live, lapsed],
        }),
      ),
    );
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: /expired/i }));
    await user.click(
      await screen.findByRole("button", { name: /forget expired/i }),
    );
    await waitFor(() => expect(expiredOnly).toBe(true));
    await waitFor(() =>
      expect(
        screen.queryByRole("button", { name: /forget expired/i }),
      ).not.toBeInTheDocument(),
    );
    expect(screen.getByText(/Blocked releases/i)).toBeInTheDocument();
  });
});
