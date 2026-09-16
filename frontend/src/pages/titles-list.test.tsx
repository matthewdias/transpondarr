import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter } from "react-router";
import { afterAll, afterEach, beforeAll, expect, it } from "vitest";
import type { Title } from "@/lib/api";
import { hiddenOnPhones } from "@/test/responsive";
import { SidebarProvider } from "@/components/ui/sidebar";
import { TitleListPage } from "@/pages/titles-list";

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const title = (over: Partial<Title>): Title => ({
  id: 1,
  title: "Signal Anomaly",
  format: "TV",
  monitored: true,
  total: 12,
  tracked: 12,
  monitored_items: 12,
  in_library: 3,
  ...over,
});

it("invites adding a title to an empty library", async () => {
  server.use(
    http.get("/api/v1/titles", () => HttpResponse.json({ titles: [] })),
  );

  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter>
        <SidebarProvider>
          <TitleListPage />
        </SidebarProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );

  expect(
    await screen.findByRole("heading", { level: 2, name: "No titles yet" }),
  ).toBeInTheDocument();
  expect(
    screen.getByText(
      "Add a series or a film from AniList to start tracking and grabbing it.",
    ),
  ).toBeInTheDocument();
  // One in the top bar, one in the empty state.
  expect(screen.getAllByRole("button", { name: /add title/i })).toHaveLength(2);
});

// The item state comes from the list endpoint, not from a per-title fetch:
// one query answers the whole page (#229).
it("renders a film's item state beside a series' count", async () => {
  server.use(
    http.get("/api/v1/titles", () =>
      HttpResponse.json({
        titles: [
          title({}),
          title({
            id: 2,
            title: "Placeholder Film",
            format: "MOVIE",
            total: 1,
            tracked: 1,
            monitored_items: 1,
            in_library: 0,
            item_status: "downloading",
          }),
        ],
      }),
    ),
  );

  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter>
        <SidebarProvider>
          <TitleListPage />
        </SidebarProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );

  expect(await screen.findByText("Downloading")).toBeInTheDocument();
  expect(screen.getByText("3 / 12")).toBeInTheDocument();
  expect(screen.queryByText("0 / 1")).not.toBeInTheDocument();
});

// #324: the Monitored column held ~94px for a pill that reads the same on almost
// every row, so the name truncated to "Placehol..." and Progress was pushed past
// the right edge. Below sm the column goes and only the exception is marked,
// inline under the name -- in the column's own word, so the two widths agree.
it("marks only an unmonitored title on phones, where the column is gone", async () => {
  server.use(
    http.get("/api/v1/titles", () =>
      HttpResponse.json({
        titles: [
          title({ id: 1, title: "Signal Anomaly", monitored: true }),
          title({ id: 2, title: "Placeholder Drift", monitored: false }),
        ],
      }),
    ),
  );

  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter>
        <SidebarProvider>
          <TitleListPage />
        </SidebarProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );

  // The column is desktop-only now, header and cells alike.
  const header = await screen.findByRole("columnheader", { name: "Monitored" });
  expect(hiddenOnPhones(header)).toBe(true);
  const pills = screen.getAllByText(/^(Monitored|Unmonitored)$/);
  const onPhones = pills.filter((el) => !hiddenOnPhones(el));

  // Exactly one pill survives a phone, and it is the unmonitored title's.
  expect(onPhones).toHaveLength(1);
  expect(onPhones[0]).toHaveTextContent("Unmonitored");
  const drifted = screen.getByRole("row", { name: /Placeholder Drift/ });
  expect(within(drifted).getAllByText("Unmonitored")).toContain(onPhones[0]);

  // Progress is not the casualty of making that room: it stays at every width.
  const progress = screen.getAllByText("3 / 12");
  expect(progress.every((el) => !hiddenOnPhones(el))).toBe(true);
});
