import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";
import type { SeriesDetail } from "@/lib/api";
import { Link, MemoryRouter, Route, Routes } from "react-router";
import { SidebarProvider } from "@/components/ui/sidebar";
import {
  MonitoringToggle,
  PinnedGroupChip,
  SeriesDetailPage,
} from "@/pages/series-detail";

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

  // The wait is only legible if you can see it without opening the dialog, and
  // an explicit 0 ("take anyone's release now") must not read as the default.
  it("shows the configured wait on the chip", () => {
    const { rerender } = renderChip(
      detail({ pinned_group: "ShinyRip", pin_delay_hours: 6 }),
    );
    expect(
      screen.getByRole("button", { name: /pin: shinyrip · 6h/i }),
    ).toBeInTheDocument();

    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    rerender(
      <QueryClientProvider client={client}>
        <PinnedGroupChip
          detail={detail({ pinned_group: "ShinyRip", pin_delay_hours: 0 })}
        />
      </QueryClientProvider>,
    );
    expect(
      screen.getByRole("button", { name: /pin: shinyrip · no wait/i }),
    ).toBeInTheDocument();

    // No override: the global default applies and the chip must not invent it.
    rerender(
      <QueryClientProvider client={client}>
        <PinnedGroupChip detail={detail({ pinned_group: "ShinyRip" })} />
      </QueryClientProvider>,
    );
    expect(
      screen.getByRole("button", { name: /^pin: shinyrip$/i }),
    ).toBeInTheDocument();
  });

  // A placeholder disappears the moment you type, so the unit has to live in a
  // label that stays on screen.
  it("names the wait field's unit in a persistent label", async () => {
    const user = userEvent.setup();
    renderChip(detail({ pinned_group: "ShinyRip", pin_delay_hours: 6 }));

    await user.click(screen.getByRole("button", { name: /pin: shinyrip/i }));
    expect(
      screen.getByRole("spinbutton", { name: /wait.*hours/i }),
    ).toBeInTheDocument();
    // The label anchors the field; the hint repeats the unit so the sentence
    // explaining blank and 0 reads without glancing back up.
    expect(screen.getByText(/wait for this group \(hours\)/i)).toBeVisible();
    expect(screen.getByText(/how many hours/i)).toBeVisible();
  });

  // Clearing the group leaves the disabled field's value in state, and the
  // server would drop it — so sending it puts a number on the wire that means
  // nothing and misreads as a wait that was set.
  it("omits the wait when the group is cleared", async () => {
    const sent: unknown[] = [];
    server.use(
      http.put("/api/v1/series/7/pinned-group", async ({ request }) => {
        sent.push(await request.json());
        return HttpResponse.json({ series_id: 7, pinned_group: "" });
      }),
    );
    const user = userEvent.setup();
    renderChip(detail({ pinned_group: "ShinyRip", pin_delay_hours: 6 }));

    await user.click(screen.getByRole("button", { name: /pin: shinyrip/i }));
    await user.clear(screen.getByRole("textbox", { name: /release group/i }));
    await user.click(screen.getByRole("button", { name: /save/i }));

    await waitFor(() => expect(sent).toEqual([{ group: "" }]));
  });

  // The server drops a delay sent without a group (it is PUT-replace: no group,
  // nothing to wait for), so the field must not accept input that goes nowhere.
  it("disables the wait field while there is no group", async () => {
    const user = userEvent.setup();
    renderChip(detail({ pinned_group: "ShinyRip", pin_delay_hours: 6 }));

    await user.click(screen.getByRole("button", { name: /pin: shinyrip/i }));
    const wait = () => screen.getByRole("spinbutton", { name: /wait/i });
    expect(wait()).toBeEnabled();

    await user.clear(screen.getByRole("textbox", { name: /release group/i }));
    expect(wait()).toBeDisabled();

    await user.type(
      screen.getByRole("textbox", { name: /release group/i }),
      "OtherGroup",
    );
    expect(wait()).toBeEnabled();
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

describe("SeriesDetailPage episode search", () => {
  const candidate = (title: string, url: string, items: number[]) => ({
    title,
    download_url: url,
    size: 700_000_000,
    seeders: 12,
    dual_audio: false,
    matched: true,
    reason: "episode matches a wanted item",
    score: 1400,
    eligible: true,
    pinned: false,
    items,
  });

  function renderPage(entry = "/series/7") {
    server.use(
      http.get("/api/v1/series/7", () =>
        HttpResponse.json(
          detail({
            items: [
              { id: 1, number: 2, in_library: false, status: "wanted" },
              { id: 2, number: 5, in_library: false, status: "wanted" },
            ],
          }),
        ),
      ),
      http.get("/api/v1/series/9", () =>
        HttpResponse.json(
          detail({
            id: 9,
            title: "Second Saga",
            items: [{ id: 3, number: 4, in_library: false, status: "wanted" }],
          }),
        ),
      ),
      http.get("/api/v1/settings", () =>
        HttpResponse.json({ automation: { mode: "on" } }),
      ),
      http.get("/api/v1/profiles", () => HttpResponse.json({ profiles: [] })),
      http.get("/api/v1/series/7/search", () =>
        HttpResponse.json({
          series: "Placeholder Saga",
          results: [
            candidate(
              "[GroupA] Placeholder Saga - 02 (1080p)",
              "magnet:?xt=urn:btih:0002",
              [2],
            ),
            candidate(
              "[GroupA] Placeholder Saga - 05 (1080p)",
              "magnet:?xt=urn:btih:0005",
              [5],
            ),
          ],
        }),
      ),
      http.get("/api/v1/series/9/search", () =>
        HttpResponse.json({
          series: "Second Saga",
          results: [
            candidate(
              "[GroupA] Second Saga - 04 (1080p)",
              "magnet:?xt=urn:btih:0004",
              [4],
            ),
          ],
        }),
      ),
    );
    const user = userEvent.setup();
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    render(
      <QueryClientProvider client={client}>
        <MemoryRouter initialEntries={[entry]}>
          <SidebarProvider>
            <Link to="/series/9">Second Saga</Link>
            <Routes>
              <Route path="/series/:id" element={<SeriesDetailPage />} />
            </Routes>
          </SidebarProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );
    return user;
  }

  // The switch to Releases is programmatic, so it must not read as the
  // series-wide intent a direct tab click carries.
  it("focuses the Releases tab on the searched episode, and a tab click clears it", async () => {
    const user = renderPage();

    const rows = await screen.findAllByRole("button", { name: "Search" });
    await user.click(rows[0]);

    expect(
      await screen.findByText("[GroupA] Placeholder Saga - 02 (1080p)"),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /covering e2/i }),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("[GroupA] Placeholder Saga - 05 (1080p)"),
    ).not.toBeInTheDocument();

    await user.click(screen.getByRole("tab", { name: /episodes/i }));
    await user.click(screen.getByRole("tab", { name: /releases/i }));

    expect(
      await screen.findByText("[GroupA] Placeholder Saga - 05 (1080p)"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("[GroupA] Placeholder Saga - 02 (1080p)"),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /covering e2/i }),
    ).not.toBeInTheDocument();

    // The header button is the series-wide intent, so it has to drop a focus a
    // row button set earlier rather than search inside it.
    await user.click(screen.getByRole("tab", { name: /episodes/i }));
    await user.click(
      (await screen.findAllByRole("button", { name: "Search" }))[0],
    );
    expect(
      await screen.findByRole("button", { name: /covering e2/i }),
    ).toBeInTheDocument();

    await user.click(screen.getByRole("tab", { name: /episodes/i }));
    await user.click(
      screen.getByRole("button", { name: /search all wanted/i }),
    );

    expect(
      await screen.findByText("[GroupA] Placeholder Saga - 05 (1080p)"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("[GroupA] Placeholder Saga - 02 (1080p)"),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /covering e2/i }),
    ).not.toBeInTheDocument();
  });

  // ?item=N is the same focus reached from another page (#150's Wanted rows),
  // so the tab has to open already filtered rather than on Episodes.
  it("opens the Releases tab focused when the URL names an episode", async () => {
    renderPage("/series/7?item=5");

    expect(
      await screen.findByText("[GroupA] Placeholder Saga - 05 (1080p)"),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("[GroupA] Placeholder Saga - 02 (1080p)"),
    ).not.toBeInTheDocument();
  });

  // Unreachable today — nothing links series to series — but a focus that
  // outlived its series would filter the new one on a number from the old.
  it("drops the focus when the page moves to another series", async () => {
    const user = renderPage();

    await user.click(
      (await screen.findAllByRole("button", { name: "Search" }))[0],
    );
    expect(
      await screen.findByRole("button", { name: /covering e2/i }),
    ).toBeInTheDocument();

    await user.click(screen.getByRole("link", { name: "Second Saga" }));

    expect(
      await screen.findByText("[GroupA] Second Saga - 04 (1080p)"),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /covering e2/i }),
    ).not.toBeInTheDocument();
  });
});

describe("MonitoringToggle", () => {
  function renderToggle(
    monitored: boolean,
    automationMode: "off" | "notify_only" | "on",
  ) {
    return render(
      <MemoryRouter>
        <MonitoringToggle
          monitored={monitored}
          automationMode={automationMode}
          onToggle={() => {}}
        />
      </MemoryRouter>,
    );
  }

  it("lets the switch speak for itself while automation is on", () => {
    renderToggle(true, "on");
    expect(screen.getByRole("switch", { name: /monitor/i })).toBeChecked();
    expect(screen.getByText("Monitored")).toBeInTheDocument();
    expect(screen.queryByText(/automation is off/i)).not.toBeInTheDocument();
  });

  // Monitored means "will be grabbed automatically", so the global kill switch
  // makes that label a promise the daemon is not keeping -- say so where the
  // promise is made rather than only in Settings.
  it("flags the global kill switch on a monitored series", () => {
    renderToggle(true, "off");
    const note = screen.getByRole("link", { name: /automation is off/i });
    expect(note).toHaveAttribute("href", "/settings");
  });

  // Notify-only keeps the promise half-made: searched and reported, not grabbed.
  it("flags a notify-only rehearsal on a monitored series", () => {
    renderToggle(true, "notify_only");
    const note = screen.getByRole("link", { name: /notify-only rehearsal/i });
    expect(note).toHaveAttribute("href", "/settings");
  });

  it("stays quiet on an unmonitored series, which the switch already explains", () => {
    renderToggle(false, "off");
    expect(screen.getByText("Unmonitored")).toBeInTheDocument();
    expect(screen.queryByText(/automation is off/i)).not.toBeInTheDocument();
  });
});
