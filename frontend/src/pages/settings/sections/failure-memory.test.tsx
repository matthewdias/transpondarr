import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";
import type { BlocklistSummary } from "@/lib/api";
import { FailureMemorySection } from "@/pages/settings/sections/failure-memory";

const summary = (over: Partial<BlocklistSummary> = {}): BlocklistSummary => ({
  blocked: 4,
  titles: 2,
  breaker: { open: false, items: 1, threshold: 5, window_minutes: 15 },
  ...over,
});

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

function renderSection(body: BlocklistSummary) {
  server.use(http.get("/api/v1/blocklist", () => HttpResponse.json(body)));
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <FailureMemorySection />
    </QueryClientProvider>,
  );
}

describe("FailureMemorySection", () => {
  it("reports how much is blocked and across how many series", async () => {
    renderSection(summary());
    expect(await screen.findByText(/4 releases/i)).toBeInTheDocument();
    // "series" is its own plural; the count line read "2 seriess" until it was.
    expect(screen.getByText(/skipped across 2 titles\./i)).toBeInTheDocument();
  });

  it("does not pluralize a single release or a lone series", async () => {
    renderSection(summary({ blocked: 1, titles: 1 }));
    expect(await screen.findByText(/1 release/)).toBeInTheDocument();
    expect(screen.getByText(/across 1 title\./i)).toBeInTheDocument();
  });

  // The diagnosis is the breaker's most valuable output: an operator who wakes
  // to a wall of failures needs to be told the client looks unwell, not left to
  // infer it from the blocklist.
  it("says so when the breaker has suppressed failure memory", async () => {
    renderSection(
      summary({
        breaker: {
          open: true,
          items: 7,
          threshold: 5,
          window_minutes: 15,
          since: new Date(Date.now() - 5 * 60_000).toISOString(),
        },
      }),
    );
    expect(await screen.findByRole("status")).toHaveTextContent(
      /not remembering/i,
    );
    expect(screen.getByText(/7 .*items/i)).toBeInTheDocument();
  });

  it("stays quiet about the breaker while it is closed", async () => {
    renderSection(summary());
    expect(await screen.findByText(/4 releases/i)).toBeInTheDocument();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  // Adjacent to a destructive action with no confirmation is how Sonarr's
  // equivalent gets misclicked (Radarr #9401), so this one asks first.
  it("clears the whole library's memory behind a confirmation", async () => {
    let cleared = false;
    renderSection(summary());
    server.use(
      http.delete("/api/v1/blocklist", () => {
        cleared = true;
        return HttpResponse.json({ cleared: 4 });
      }),
      http.get("/api/v1/blocklist", () =>
        HttpResponse.json(
          cleared ? summary({ blocked: 0, titles: 0 }) : summary(),
        ),
      ),
    );
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: /clear/i }));
    expect(cleared).toBe(false);
    await user.click(await screen.findByRole("button", { name: /forget/i }));

    await waitFor(() => expect(cleared).toBe(true));
    expect(await screen.findByText(/nothing is blocked/i)).toBeInTheDocument();
  });

  it("offers nothing to clear when the library has no memory", async () => {
    renderSection(summary({ blocked: 0, titles: 0 }));
    expect(await screen.findByText(/nothing is blocked/i)).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /clear/i }),
    ).not.toBeInTheDocument();
  });
});
