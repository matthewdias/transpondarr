import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter } from "react-router";
import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";
import type {
  ActivityEvent,
  ActivityQueue,
  ActivityUnmatched,
  QueueItem,
} from "@/lib/api";
import { SidebarProvider } from "@/components/ui/sidebar";
import { ActivityPage } from "@/pages/activity";

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const queueItem = (over: Partial<QueueItem>): QueueItem => ({
  id: 1,
  title_id: 7,
  title: "Signal Anomaly",
  item_number: 4,
  release_title: "[FakeGroup] Signal Anomaly - 04 (1080p) [ABCD1234].mkv",
  infohash: "aaaa",
  status: "downloading",
  created_at: "2026-07-30 12:00:00",
  ...over,
});

const historyEvent = (over: Partial<ActivityEvent>): ActivityEvent => ({
  id: 1,
  title_id: 7,
  title: "Signal Anomaly",
  item_number: 3,
  release_title: "[FakeGroup] Signal Anomaly - 03 (1080p) [ABCD1234].mkv",
  infohash: "bbbb",
  status: "grabbed",
  created_at: "2026-07-29 12:00:00",
  ...over,
});

const noUnmatched: ActivityUnmatched = {
  items: [],
  client_ok: true,
  scoped: true,
};

function useHandlers(
  queue: ActivityQueue,
  pages: Record<string, { events: ActivityEvent[]; next_cursor?: string }>,
  onHistory?: (cursor: string) => void,
  unmatched: ActivityUnmatched = noUnmatched,
) {
  server.use(
    http.get("/api/v1/activity/queue", () => HttpResponse.json(queue)),
    http.get("/api/v1/activity/unmatched", () => HttpResponse.json(unmatched)),
    http.get("/api/v1/activity/history", ({ request }) => {
      const cursor = new URL(request.url).searchParams.get("cursor") ?? "";
      onHistory?.(cursor);
      return HttpResponse.json(pages[cursor] ?? { events: [] });
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
          <ActivityPage />
        </SidebarProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return client;
}

describe("ActivityPage", () => {
  it("shows queue rows with live client state and history rows with details", async () => {
    useHandlers(
      {
        client_ok: true,
        items: [
          queueItem({ id: 1, client_state: "paused", progress: 0.42 }),
          queueItem({
            id: 2,
            item_number: 5,
            title_id: 9,
            title: "Dusty Archive",
            status: "stuck",
            import_error: "import failed: disk full",
          }),
        ],
      },
      {
        "": {
          events: [
            historyEvent({ id: 11 }),
            historyEvent({
              id: 12,
              status: "failed",
              detail: "the download vanished from the client",
            }),
          ],
        },
      },
    );

    renderPage();

    // Queue: the paused row carries the live state and progress.
    expect(await screen.findByText("Paused")).toBeInTheDocument();
    expect(screen.getByText(/42%/)).toBeInTheDocument();
    const links = screen.getAllByRole("link", { name: "Signal Anomaly" });
    expect(links[0]).toHaveAttribute("href", "/series/7");

    // Queue: the stuck row names its import error.
    expect(screen.getByText("import failed: disk full")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Dusty Archive" })).toHaveAttribute(
      "href",
      "/series/9",
    );

    // History: past-tense verbs, failure detail carried through.
    expect(screen.getByText(/Grabbed/)).toBeInTheDocument();
    expect(screen.getByText(/Failed/)).toBeInTheDocument();
    expect(
      screen.getByText("the download vanished from the client"),
    ).toBeInTheDocument();

    // With everything on one page there is nothing more to load.
    expect(
      screen.queryByRole("button", { name: /load more/i }),
    ).not.toBeInTheDocument();
  });

  it("loads more history through the cursor until exhausted", async () => {
    const cursors: string[] = [];
    useHandlers(
      { client_ok: true, items: [] },
      {
        "": { events: [historyEvent({ id: 11 })], next_cursor: "c1" },
        c1: { events: [historyEvent({ id: 12, item_number: 2 })] },
      },
      (cursor) => cursors.push(cursor),
    );

    renderPage();

    expect(await screen.findByText(/Nothing downloading/i)).toBeInTheDocument();
    const more = await screen.findByRole("button", { name: /load more/i });
    await userEvent.setup().click(more);

    await waitFor(() => expect(cursors).toContain("c1"));
    expect(await screen.findByText(/Episode 2/)).toBeInTheDocument();
    // Both pages stay on screen; the button is gone once the cursor runs out.
    expect(screen.getByText(/Episode 3/)).toBeInTheDocument();
    await waitFor(() =>
      expect(
        screen.queryByRole("button", { name: /load more/i }),
      ).not.toBeInTheDocument(),
    );
  });

  // History only fetches once, so a settle the queue reflects (an item leaving)
  // must invalidate it; progress ticks alone must not.
  it("refreshes history when a queue item settles, not on progress ticks", async () => {
    let queuePayload: ActivityQueue = {
      client_ok: true,
      items: [queueItem({ id: 1, client_state: "downloading", progress: 0.1 })],
    };
    let historyCalls = 0;
    server.use(
      http.get("/api/v1/activity/queue", () => HttpResponse.json(queuePayload)),
      http.get("/api/v1/activity/unmatched", () =>
        HttpResponse.json(noUnmatched),
      ),
      http.get("/api/v1/activity/history", () => {
        historyCalls++;
        return HttpResponse.json({ events: [historyEvent({ id: 11 })] });
      }),
    );

    const client = renderPage();
    expect(await screen.findByText(/Episode 4/)).toBeInTheDocument();
    await waitFor(() => expect(historyCalls).toBe(1));

    // A poll that only moves progress leaves history alone.
    queuePayload = {
      client_ok: true,
      items: [queueItem({ id: 1, client_state: "downloading", progress: 0.9 })],
    };
    await act(() => client.refetchQueries({ queryKey: ["activity-queue"] }));
    expect(await screen.findByText(/90%/)).toBeInTheDocument();
    expect(historyCalls).toBe(1);

    // The item settles out of the queue; history refetches.
    queuePayload = { client_ok: true, items: [] };
    await act(() => client.refetchQueries({ queryKey: ["activity-queue"] }));
    expect(await screen.findByText(/Nothing downloading/i)).toBeInTheDocument();
    await waitFor(() => expect(historyCalls).toBe(2));
  });

  it("says when the download client is unreachable instead of hiding the queue", async () => {
    useHandlers(
      { client_ok: false, items: [queueItem({ id: 1 })] },
      { "": { events: [] } },
    );

    renderPage();

    expect(
      await screen.findByText(/download client unreachable/i),
    ).toBeInTheDocument();
    // The grab-state row still renders, without live state.
    expect(screen.getByText(/Episode 4/)).toBeInTheDocument();
    expect(screen.queryByText("Paused")).not.toBeInTheDocument();
  });
});

describe("unmatched downloads", () => {
  const orphan = {
    infohash: "eeee5555",
    name: "[FakeGroup] Signal Anomaly - 04 (1080p) [ABCD1234]",
    client_state: "downloading" as const,
    progress: 0.25,
    save_path: "/downloads",
    size: 734003200,
    added_at: new Date(Date.now() - 3 * 86_400_000).toISOString(),
  };

  // A rare state: an always-present empty section would be noise on every visit.
  it("stays out of the way when nothing is unmatched", async () => {
    useHandlers({ client_ok: true, items: [] }, { "": { events: [] } });

    renderPage();

    expect(await screen.findByText(/Nothing downloading/i)).toBeInTheDocument();
    expect(screen.queryByText(/Unmatched downloads/i)).not.toBeInTheDocument();
  });

  // No grab row stands behind these, so the row itself has to carry enough to
  // recognise the torrent by: how big it is and how long it has been sitting.
  it("identifies the orphan by size and age", async () => {
    useHandlers(
      { client_ok: true, items: [] },
      { "": { events: [] } },
      undefined,
      {
        items: [orphan],
        client_ok: true,
        scoped: true,
      },
    );

    renderPage();

    expect(await screen.findByText(/700 MB/)).toBeInTheDocument();
    expect(screen.getByText(/3d ago/)).toBeInTheDocument();
  });

  // Keeping the payload and finding it by hand is the alternative to deleting
  // it, so the confirm step is where the path has to be readable.
  it("names the payload's location in the confirm dialog", async () => {
    useHandlers(
      { client_ok: true, items: [] },
      { "": { events: [] } },
      undefined,
      { items: [orphan], client_ok: true, scoped: true },
    );

    renderPage();

    await userEvent
      .setup()
      .click(await screen.findByRole("button", { name: /^Remove$/ }));
    expect(await screen.findByText("/downloads")).toBeInTheDocument();
  });

  it("lists the orphan and removes it with its data by default", async () => {
    let removed: { hash: string; deleteData: string | null } | undefined;
    useHandlers(
      { client_ok: true, items: [] },
      { "": { events: [] } },
      undefined,
      {
        items: [orphan],
        client_ok: true,
        scoped: true,
      },
    );
    server.use(
      http.delete("/api/v1/activity/unmatched/:hash", ({ params, request }) => {
        removed = {
          hash: String(params.hash),
          deleteData: new URL(request.url).searchParams.get("delete_data"),
        };
        return new HttpResponse(null, { status: 204 });
      }),
    );

    renderPage();

    expect(await screen.findByText(/Unmatched downloads/i)).toBeInTheDocument();
    expect(screen.getByText(orphan.name)).toBeInTheDocument();
    expect(screen.getByText(/25%/)).toBeInTheDocument();

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /^Remove$/ }));
    await user.click(
      await screen.findByRole("button", { name: /Remove download/ }),
    );

    await waitFor(() =>
      expect(removed).toEqual({ hash: "eeee5555", deleteData: "true" }),
    );
  });

  it("keeps the data on disk when the checkbox is cleared", async () => {
    let deleteData: string | null = null;
    useHandlers(
      { client_ok: true, items: [] },
      { "": { events: [] } },
      undefined,
      {
        items: [orphan],
        client_ok: true,
        scoped: true,
      },
    );
    server.use(
      http.delete("/api/v1/activity/unmatched/:hash", ({ request }) => {
        deleteData = new URL(request.url).searchParams.get("delete_data");
        return new HttpResponse(null, { status: 204 });
      }),
    );

    renderPage();

    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: /^Remove$/ }));
    await user.click(await screen.findByRole("checkbox"));
    await user.click(screen.getByRole("button", { name: /Remove download/ }));

    await waitFor(() => expect(deleteData).toBe("false"));
  });
});

describe("fixing a deferred import", () => {
  const payload = {
    release_title: "[FakeGroup] Signal Anomaly - 01-02 [Batch]",
    infohash: "cccc",
    items: [
      { grab_id: 1, item_number: 1, status: "imported" },
      { grab_id: 3, item_number: 2, status: "import_deferred" },
    ],
    files: [
      {
        path: "b1946ac92492d2347c6235b4d2611184.mkv",
        episode_start: 0,
        episode_end: 0,
        absolute_episode: 0,
        batch: false,
        version: 0,
        repack: false,
        suggested_item: 0,
      },
    ],
    archives: [],
  };

  it("offers the fix only on deferred rows and submits the assignment", async () => {
    let posted: unknown;
    useHandlers(
      {
        client_ok: true,
        items: [
          queueItem({ id: 1 }),
          queueItem({ id: 3, item_number: 2, status: "deferred" }),
        ],
      },
      { "": { events: [] } },
    );
    server.use(
      http.get("/api/v1/activity/queue/3/payload", () =>
        HttpResponse.json(payload),
      ),
      http.post(
        "/api/v1/activity/queue/3/retry-import",
        async ({ request }) => {
          posted = await request.json();
          return HttpResponse.json({
            results: [{ item_number: 2, outcome: "imported" }],
          });
        },
      ),
    );

    renderPage();

    // One button, on the deferred row only — a downloading row has nothing to fix.
    const fix = await screen.findAllByRole("button", { name: /Fix import/ });
    expect(fix).toHaveLength(1);
    await userEvent.click(fix[0]);

    // The file's parse is shown, because "nothing was read" is why it needs a human.
    expect(
      await screen.findByText("b1946ac92492d2347c6235b4d2611184.mkv"),
    ).toBeInTheDocument();
    expect(screen.getByText("no episode number read")).toBeInTheDocument();

    await userEvent.click(
      screen.getByRole("combobox", {
        name: /Episode for b1946ac92492d2347c6235b4d2611184.mkv/,
      }),
    );
    await userEvent.click(
      await screen.findByRole("option", { name: /Episode 2/ }),
    );
    await userEvent.click(screen.getByRole("button", { name: "Import" }));

    await waitFor(() =>
      expect(posted).toEqual({
        assignments: [
          { file: "b1946ac92492d2347c6235b4d2611184.mkv", item_number: 2 },
        ],
      }),
    );
  });

  // Nothing unpacks archives, so the dialog has to say what it found and what to
  // do — an empty file list was the dead end this whole path exists to end.
  it("names the archive it cannot unpack and still lets the retry run", async () => {
    let posted: unknown;
    useHandlers(
      { client_ok: true, items: [queueItem({ id: 3, status: "deferred" })] },
      { "": { events: [] } },
    );
    server.use(
      http.get("/api/v1/activity/queue/3/payload", () =>
        HttpResponse.json({
          ...payload,
          items: [{ grab_id: 3, item_number: 2, status: "import_deferred" }],
          files: [],
          archives: [
            {
              path: "placeholder.saga.s01e02.1080p.web.h264-synth.rar",
              parts: 3,
            },
          ],
        }),
      ),
      http.post(
        "/api/v1/activity/queue/3/retry-import",
        async ({ request }) => {
          posted = await request.json();
          return HttpResponse.json({
            results: [{ item_number: 2, outcome: "imported" }],
          });
        },
      ),
    );

    renderPage();
    await userEvent.click(
      (await screen.findAllByRole("button", { name: /Fix import/ }))[0],
    );

    expect(
      await screen.findByText(
        "placeholder.saga.s01e02.1080p.web.h264-synth.rar",
      ),
    ).toBeInTheDocument();
    expect(screen.getByText(/3 parts/)).toBeInTheDocument();
    expect(screen.getByText(/does not unpack archives/i)).toBeInTheDocument();
    // Nothing to assign, so the file picker must not be offered at all.
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();

    // The point of the dialog: extract in place, then retry re-runs the mapping.
    await userEvent.click(screen.getByRole("button", { name: "Retry import" }));
    await waitFor(() => expect(posted).toEqual({ assignments: [] }));
  });

  // A preselected suggestion left out of the request would be re-derived, and an
  // override changes how the mapping runs — so the dialog must send what it shows.
  it("submits preselected suggestions the user never touched", async () => {
    let posted: unknown;
    useHandlers(
      { client_ok: true, items: [queueItem({ id: 3, status: "deferred" })] },
      { "": { events: [] } },
    );
    server.use(
      http.get("/api/v1/activity/queue/3/payload", () =>
        HttpResponse.json({
          ...payload,
          items: [
            { grab_id: 3, item_number: 2, status: "import_deferred" },
            { grab_id: 4, item_number: 5, status: "import_deferred" },
          ],
          files: [
            ...payload.files,
            {
              path: "[SynthSubs] Signal Anomaly - 05 [1080p].mkv",
              episode_start: 5,
              episode_end: 5,
              absolute_episode: 0,
              batch: false,
              version: 0,
              repack: false,
              suggested_item: 5,
            },
          ],
        }),
      ),
      http.post(
        "/api/v1/activity/queue/3/retry-import",
        async ({ request }) => {
          posted = await request.json();
          return HttpResponse.json({
            results: [{ item_number: 2, outcome: "imported" }],
          });
        },
      ),
    );

    renderPage();
    await userEvent.click(
      (await screen.findAllByRole("button", { name: /Fix import/ }))[0],
    );
    await userEvent.click(
      await screen.findByRole("combobox", {
        name: /Episode for b1946ac92492d2347c6235b4d2611184.mkv/,
      }),
    );
    await userEvent.click(
      await screen.findByRole("option", { name: /Episode 2/ }),
    );
    await userEvent.click(screen.getByRole("button", { name: "Import" }));

    await waitFor(() =>
      expect(posted).toEqual({
        assignments: [
          { file: "b1946ac92492d2347c6235b4d2611184.mkv", item_number: 2 },
          {
            file: "[SynthSubs] Signal Anomaly - 05 [1080p].mkv",
            item_number: 5,
          },
        ],
      }),
    );
  });
});
