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

  // Saving a value equal to the current pin is a no-op request; the empty-input
  // case additionally toasted "Pin cleared" when there was nothing to clear.
  it("disables Save until the value differs from the current pin", async () => {
    const user = userEvent.setup();
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const { rerender } = render(
      <QueryClientProvider client={client}>
        <PinnedGroupChip detail={detail({})} />
      </QueryClientProvider>,
    );
    const save = () => screen.getByRole("button", { name: /save/i });
    const input = () => screen.getByRole("textbox", { name: /release group/i });

    await user.click(screen.getByRole("button", { name: /pin group/i }));
    expect(save()).toBeDisabled();
    await user.type(input(), "  ");
    expect(save()).toBeDisabled();
    await user.type(input(), "ShinyRip");
    expect(save()).toBeEnabled();
    await user.keyboard("{Escape}");

    rerender(
      <QueryClientProvider client={client}>
        <PinnedGroupChip detail={detail({ pinned_group: "ShinyRip" })} />
      </QueryClientProvider>,
    );
    await user.click(screen.getByRole("button", { name: /pin: shinyrip/i }));
    expect(save()).toBeDisabled();
  });

  // #62: the wait is per-series, and a blank field means "use the global
  // default" rather than "wait zero hours".
  it("sends a per-series wait, and omits it when blank", async () => {
    const sent: unknown[] = [];
    server.use(
      http.put("/api/v1/series/7/pinned-group", async ({ request }) => {
        sent.push(await request.json());
        return HttpResponse.json({ series_id: 7, pinned_group: "ShinyRip" });
      }),
    );
    const user = userEvent.setup();
    renderChip(detail({ pinned_group: "ShinyRip", pin_delay_hours: 6 }));

    await user.click(screen.getByRole("button", { name: /pin: shinyrip/i }));
    const wait = screen.getByRole("spinbutton", { name: /wait/i });
    expect(wait).toHaveValue(6);
    await user.clear(wait);
    await user.type(wait, "12");
    await user.click(screen.getByRole("button", { name: /save/i }));
    await waitFor(() =>
      expect(sent).toEqual([{ group: "ShinyRip", delay_hours: 12 }]),
    );

    await user.click(screen.getByRole("button", { name: /pin: shinyrip/i }));
    await user.clear(screen.getByRole("spinbutton", { name: /wait/i }));
    await user.click(screen.getByRole("button", { name: /save/i }));
    await waitFor(() => expect(sent).toHaveLength(2));
    expect(sent[1]).toEqual({ group: "ShinyRip" });
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
