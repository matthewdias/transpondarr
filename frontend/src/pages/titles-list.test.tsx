import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter } from "react-router";
import { afterAll, afterEach, beforeAll, expect, it } from "vitest";
import type { Title } from "@/lib/api";
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

// The state reaches the row from the list endpoint, not from a per-title fetch:
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
