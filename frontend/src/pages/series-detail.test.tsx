import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";
import type { SeriesDetail } from "@/lib/api";
import { PinnedGroupChip } from "@/pages/series-detail";

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const detail = (over: Partial<SeriesDetail>): SeriesDetail => ({
  id: 7,
  title: "Placeholder Saga",
  format: "TV",
  monitored: true,
  quality_profile_id: 1,
  items: [],
  ...over,
});

function renderChip(d: SeriesDetail) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <PinnedGroupChip detail={d} />
    </QueryClientProvider>,
  );
}

describe("PinnedGroupChip", () => {
  it("saves a typed group through the pin endpoint", async () => {
    let sent: unknown;
    server.use(
      http.put("/api/v1/series/7/pinned-group", async ({ request }) => {
        sent = await request.json();
        return HttpResponse.json({ series_id: 7, pinned_group: "ShinyRip" });
      }),
    );
    const user = userEvent.setup();
    renderChip(detail({}));

    // Unset state invites a pin rather than showing an empty value.
    await user.click(screen.getByRole("button", { name: /pin group/i }));
    await user.type(
      screen.getByRole("textbox", { name: /release group/i }),
      "ShinyRip",
    );
    await user.click(screen.getByRole("button", { name: /save/i }));
    await waitFor(() => expect(sent).toEqual({ group: "ShinyRip" }));
    // The dialog closes once the save lands.
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
  });

  it("shows the current pin and clears it", async () => {
    let sent: unknown;
    server.use(
      http.put("/api/v1/series/7/pinned-group", async ({ request }) => {
        sent = await request.json();
        return HttpResponse.json({ series_id: 7 });
      }),
    );
    const user = userEvent.setup();
    renderChip(detail({ pinned_group: "ShinyRip" }));

    await user.click(screen.getByRole("button", { name: /pin: shinyrip/i }));
    await user.click(screen.getByRole("button", { name: /clear/i }));
    await waitFor(() => expect(sent).toEqual({ group: "" }));
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
  });
});
