import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter } from "react-router";
import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";
import type { ActivityEvent, ActivityQueue, QueueItem } from "@/lib/api";
import { SidebarProvider } from "@/components/ui/sidebar";
import { ActivityPage } from "@/pages/activity";

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const queueItem = (over: Partial<QueueItem>): QueueItem => ({
  id: 1,
  series_id: 7,
  series_title: "Signal Anomaly",
  item_number: 4,
  release_title: "[FakeGroup] Signal Anomaly - 04 (1080p) [ABCD1234].mkv",
  infohash: "aaaa",
  status: "downloading",
  created_at: "2026-07-30 12:00:00",
  ...over,
});

const historyEvent = (over: Partial<ActivityEvent>): ActivityEvent => ({
  id: 1,
  series_id: 7,
  series_title: "Signal Anomaly",
  item_number: 3,
  release_title: "[FakeGroup] Signal Anomaly - 03 (1080p) [ABCD1234].mkv",
  infohash: "bbbb",
  status: "grabbed",
  created_at: "2026-07-29 12:00:00",
  ...over,
});

function useHandlers(
  queue: ActivityQueue,
  pages: Record<string, { events: ActivityEvent[]; next_cursor?: string }>,
  onHistory?: (cursor: string) => void,
) {
  server.use(
    http.get("/api/v1/activity/queue", () => HttpResponse.json(queue)),
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
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <SidebarProvider>
          <ActivityPage />
        </SidebarProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
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
            series_id: 9,
            series_title: "Dusty Archive",
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
