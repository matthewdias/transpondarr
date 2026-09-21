import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, delay, http } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";
import type { TitleDetail } from "@/lib/api";
import { Link, MemoryRouter, Route, Routes } from "react-router";
import { SidebarProvider } from "@/components/ui/sidebar";
import {
  MonitoringToggle,
  PinnedGroupChip,
  ProfilePicker,
  TitleDetailPage,
} from "@/pages/title-detail";

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const detail = (over: Partial<TitleDetail>): TitleDetail => ({
  id: 7,
  title: "Placeholder Saga",
  format: "TV",
  monitored: true,
  quality_profile_id: 1,
  items: [],
  ...over,
});

function renderChip(d: TitleDetail) {
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
      http.put("/api/v1/titles/7/pinned-group", async ({ request }) => {
        sent = await request.json();
        return HttpResponse.json({ title_id: 7, pinned_group: "ShinyRip" });
      }),
    );
    const user = userEvent.setup();
    renderChip(detail({}));

    // Unset pin state prompts for a pin rather than showing an empty value.
    await user.click(
      screen.getByRole("button", { name: /pin release group/i }),
    );
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

    await user.click(
      screen.getByRole("button", { name: /pin release group/i }),
    );
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

  // #62: the wait is per-title, and a blank field means "use the global
  // default" rather than "wait zero hours".
  it("sends a per-title wait, and omits it when blank", async () => {
    const sent: unknown[] = [];
    server.use(
      http.put("/api/v1/titles/7/pinned-group", async ({ request }) => {
        sent.push(await request.json());
        return HttpResponse.json({ title_id: 7, pinned_group: "ShinyRip" });
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

    // No override: the global default applies and the chip must not show it.
    rerender(
      <QueryClientProvider client={client}>
        <PinnedGroupChip detail={detail({ pinned_group: "ShinyRip" })} />
      </QueryClientProvider>,
    );
    expect(
      screen.getByRole("button", { name: /^pin: shinyrip$/i }),
    ).toBeInTheDocument();
  });

  // A placeholder disappears the moment you type, so the unit has to be in a
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

  // Clearing the group leaves the disabled field's value in React state, and the
  // server would drop it — so sending it puts a number on the wire that means
  // nothing and misreads as a wait that was set.
  it("omits the wait when the group is cleared", async () => {
    const sent: unknown[] = [];
    server.use(
      http.put("/api/v1/titles/7/pinned-group", async ({ request }) => {
        sent.push(await request.json());
        return HttpResponse.json({ title_id: 7, pinned_group: "" });
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
      http.put("/api/v1/titles/7/pinned-group", async ({ request }) => {
        sent = await request.json();
        return HttpResponse.json({ title_id: 7 });
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

describe("ProfilePicker", () => {
  function renderPicker(d: TitleDetail) {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    return render(
      <QueryClientProvider client={client}>
        <MemoryRouter>
          <ProfilePicker detail={d} />
        </MemoryRouter>
      </QueryClientProvider>,
    );
  }

  const picker = () =>
    screen.findByRole("combobox", { name: /quality profile/i });

  // The picker's query starts only once the header is painted, so rendering
  // nothing while it loads shifted the chips row on every visit.
  it("reserves the chip's place while the profiles load", async () => {
    server.use(
      http.get("/api/v1/profiles", async () => {
        await delay(20);
        return HttpResponse.json({ profiles: [{ id: 1, name: "Default" }] });
      }),
    );
    const { container } = renderPicker(detail({}));

    expect(
      container.querySelector('[data-slot="skeleton"]'),
    ).toBeInTheDocument();
    expect(await picker()).toBeInTheDocument();
    expect(
      container.querySelector('[data-slot="skeleton"]'),
    ).not.toBeInTheDocument();
  });

  // A failed fetch used to be indistinguishable from a title with no profile
  // control, and nothing let you ask again.
  it("reports the failure and retries when the profiles cannot be read", async () => {
    server.use(
      http.get("/api/v1/profiles", () =>
        HttpResponse.json(
          { status: 500, detail: "database is locked" },
          { status: 500 },
        ),
      ),
    );
    const user = userEvent.setup();
    renderPicker(detail({}));

    // The cause opens by tap or keyboard; a title attribute opened on neither.
    const why = await screen.findByRole("button", {
      name: "Profile unavailable",
    });
    expect(why).not.toHaveAttribute("title");
    await user.click(why);
    expect(await screen.findByRole("status")).toHaveTextContent(
      "database is locked",
    );

    const retry = screen.getByRole("button", {
      name: "Retry loading profiles",
    });
    server.use(
      http.get("/api/v1/profiles", () =>
        HttpResponse.json({ profiles: [{ id: 1, name: "Default" }] }),
      ),
    );
    await user.click(retry);
    expect(await picker()).toBeInTheDocument();
  });

  it("words a network failure as the reason the profiles are unavailable", async () => {
    server.use(http.get("/api/v1/profiles", () => HttpResponse.error()));
    const user = userEvent.setup();
    renderPicker(detail({}));

    await user.click(
      await screen.findByRole("button", { name: "Profile unavailable" }),
    );
    expect(await screen.findByRole("status")).toHaveTextContent(
      "Transpondarr didn’t respond. Check that it’s running.",
    );
  });

  it("points at Settings when there are no profiles to pick", async () => {
    server.use(
      http.get("/api/v1/profiles", () => HttpResponse.json({ profiles: [] })),
    );
    renderPicker(detail({}));

    expect(
      await screen.findByRole("link", { name: /no profiles/i }),
    ).toHaveAttribute("href", "/settings");
  });

  it("shows the assigned profile and assigns the one picked", async () => {
    let sent: unknown;
    server.use(
      http.get("/api/v1/profiles", () =>
        HttpResponse.json({
          profiles: [
            { id: 1, name: "Default" },
            { id: 2, name: "Archival" },
          ],
        }),
      ),
      http.put("/api/v1/titles/7/profile", async ({ request }) => {
        sent = await request.json();
        return HttpResponse.json({ title_id: 7, profile_id: 2 });
      }),
    );
    const user = userEvent.setup();
    renderPicker(detail({ quality_profile_id: 1 }));

    const trigger = await picker();
    expect(trigger).toHaveTextContent("Default");
    await user.click(trigger);
    await user.click(await screen.findByRole("option", { name: "Archival" }));

    await waitFor(() => expect(sent).toEqual({ profile_id: 2 }));
  });
});

describe("TitleDetailPage episode search", () => {
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

  function renderPage(entry = "/titles/7") {
    server.use(
      http.get("/api/v1/titles/7", () =>
        HttpResponse.json(
          detail({
            items: [
              {
                id: 1,
                number: 2,
                in_library: false,
                monitored: true,
                status: "wanted",
              },
              {
                id: 2,
                number: 5,
                in_library: false,
                monitored: true,
                status: "wanted",
              },
            ],
          }),
        ),
      ),
      http.get("/api/v1/titles/9", () =>
        HttpResponse.json(
          detail({
            id: 9,
            title: "Second Saga",
            items: [
              {
                id: 3,
                number: 4,
                in_library: false,
                monitored: true,
                status: "wanted",
              },
            ],
          }),
        ),
      ),
      http.get("/api/v1/settings", () =>
        HttpResponse.json({ automation: { mode: "on" } }),
      ),
      http.get("/api/v1/profiles", () => HttpResponse.json({ profiles: [] })),
      http.get("/api/v1/titles/7/search", () =>
        HttpResponse.json({
          title: "Placeholder Saga",
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
      http.get("/api/v1/titles/9/search", () =>
        HttpResponse.json({
          title: "Second Saga",
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
            <Link to="/titles/9">Second Saga</Link>
            <Routes>
              <Route path="/titles/:id" element={<TitleDetailPage />} />
            </Routes>
          </SidebarProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );
    return user;
  }

  // The switch to Releases is programmatic, so it must not read as the
  // title-wide intent of a direct tab click.
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

    // The header button is the title-wide intent, so it has to drop a focus a
    // row button set earlier rather than search inside it.
    await user.click(screen.getByRole("tab", { name: /episodes/i }));
    await user.click(
      (await screen.findAllByRole("button", { name: "Search" }))[0],
    );
    expect(
      await screen.findByRole("button", { name: /covering e2/i }),
    ).toBeInTheDocument();

    await user.click(screen.getByRole("tab", { name: /episodes/i }));
    await user.click(screen.getByRole("button", { name: "Search all" }));

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

  // The pair another page sends (#150's Wanted rows): ?tab opens Releases and
  // ?item filters it, each doing exactly one job (#231).
  it("opens the Releases tab focused when the URL names a tab and an episode", async () => {
    renderPage("/titles/7?tab=releases&item=5");

    expect(
      await screen.findByText("[GroupA] Placeholder Saga - 05 (1080p)"),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("[GroupA] Placeholder Saga - 02 (1080p)"),
    ).not.toBeInTheDocument();
  });

  it("opens the Releases tab unfiltered when the URL names only the tab", async () => {
    renderPage("/titles/7?tab=releases");

    expect(
      await screen.findByText("[GroupA] Placeholder Saga - 05 (1080p)"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("[GroupA] Placeholder Saga - 02 (1080p)"),
    ).toBeInTheDocument();
    expect(screen.queryByText(/covering e/i)).not.toBeInTheDocument();
  });

  // ?item no longer implies the tab, so it lands on the format's own tab and
  // the focus it sets is not on screen -- never a half-focused one.
  it("leaves the landing tab alone when the URL names only an item", async () => {
    renderPage("/titles/7?item=5");

    expect(
      await screen.findByRole("columnheader", { name: "Ep" }),
    ).toBeVisible();
    expect(screen.queryByText(/covering e/i)).not.toBeInTheDocument();
    expect(
      screen.queryByText("[GroupA] Placeholder Saga - 05 (1080p)"),
    ).not.toBeInTheDocument();
  });

  // Unreachable today — nothing links title to title — but a focus that
  // outlasted its title would filter the new one on a number from the old.
  it("drops the focus when the page moves to another title", async () => {
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

  it("shows only the switch while automation is on", () => {
    renderToggle(true, "on");
    expect(screen.getByRole("switch", { name: /monitor/i })).toBeChecked();
    expect(screen.getByText("Monitored")).toBeInTheDocument();
    expect(screen.queryByText(/automation is off/i)).not.toBeInTheDocument();
  });

  // Monitored means "will be grabbed automatically", so the global kill switch
  // makes that label false -- show that beside the label rather than only in
  // Settings.
  it("flags the global kill switch on a monitored title", () => {
    renderToggle(true, "off");
    const note = screen.getByRole("link", { name: /automation is off/i });
    expect(note).toHaveAttribute("href", "/settings");
  });

  // Notify-only makes the label half true: searched and reported, not grabbed.
  it("flags notify-only automation on a monitored title", () => {
    renderToggle(true, "notify_only");
    const note = screen.getByRole("link", {
      name: /automation is notify only/i,
    });
    expect(note).toHaveAttribute("href", "/settings");
  });

  it("stays quiet on an unmonitored title, which the switch already explains", () => {
    renderToggle(false, "off");
    expect(screen.getByText("Unmonitored")).toBeInTheDocument();
    expect(screen.queryByText(/automation is off/i)).not.toBeInTheDocument();
  });
});

// Format is the discriminator, never item count (#208), so these two mounts are
// the whole rule: a one-item film loses the episodes table, a one-episode OVA
// keeps it.
describe("TitleDetailPage movie surface", () => {
  const oneItem = [
    {
      id: 1,
      number: 1,
      in_library: false,
      monitored: true,
      status: "wanted" as const,
    },
  ];

  function renderPage(over: Partial<TitleDetail>) {
    server.use(
      http.get("/api/v1/titles/7", () =>
        HttpResponse.json(detail({ items: oneItem, ...over })),
      ),
      http.get("/api/v1/settings", () =>
        HttpResponse.json({ automation: { mode: "on" } }),
      ),
      http.get("/api/v1/profiles", () => HttpResponse.json({ profiles: [] })),
    );
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    return render(
      <QueryClientProvider client={client}>
        <MemoryRouter initialEntries={["/titles/7"]}>
          <SidebarProvider>
            <Routes>
              <Route path="/titles/:id" element={<TitleDetailPage />} />
            </Routes>
          </SidebarProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );
  }

  // #321: cover art that fails to load left the header an empty bordered box.
  it("falls back to the letter placeholder for a cover that fails to load", async () => {
    const { container } = renderPage({
      title: "Placeholder Film",
      cover_url: "https://cdn.example/gone.jpg",
    });

    const cover = () => container.querySelector("img");
    await waitFor(() => expect(cover()).not.toBeNull());
    fireEvent.error(cover()!);

    expect(cover()).toBeNull();
    expect(screen.getByText("P")).toBeInTheDocument();
  });

  it("gives a film a status card and its year, never an episodes table", async () => {
    renderPage({ format: "MOVIE", title: "Placeholder Film", year: 2019 });

    // The card is the landing tab, so the acquisition state reads without a click.
    expect(await screen.findByRole("tab", { name: /status/i })).toBeVisible();
    expect(screen.getByText("Wanted")).toBeInTheDocument();
    expect(
      screen.queryByRole("tab", { name: /episodes/i }),
    ).not.toBeInTheDocument();
    expect(screen.queryByRole("columnheader", { name: "Ep" })).toBeNull();
    // The chip reads the year a film is identified by, not "1 episodes".
    expect(screen.getByText("2019")).toBeInTheDocument();
    expect(screen.queryByText(/1 episodes?$/)).not.toBeInTheDocument();
    // Releases and History are untouched.
    expect(screen.getByRole("tab", { name: /releases/i })).toBeVisible();
    expect(screen.getByRole("tab", { name: /history/i })).toBeVisible();
  });

  it("leaves a single-episode OVA its episodes table", async () => {
    renderPage({ format: "OVA", title: "Placeholder OVA" });

    expect(await screen.findByRole("tab", { name: /episodes/i })).toBeVisible();
    expect(
      screen.queryByRole("tab", { name: /^status/i }),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: "Ep" })).toBeVisible();
    // One item, so the count reads as one -- "1 episodes" was the old bug.
    expect(screen.getByText("1 episode")).toBeInTheDocument();
  });
});

describe("TitleDetailPage missing title", () => {
  it("says the title is gone and links back to the library", async () => {
    server.use(
      http.get("/api/v1/titles/7", () =>
        HttpResponse.json(
          { status: 404, detail: "title not found" },
          { status: 404 },
        ),
      ),
      http.get("/api/v1/settings", () =>
        HttpResponse.json({ automation: { mode: "on" } }),
      ),
    );
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    render(
      <QueryClientProvider client={client}>
        <MemoryRouter initialEntries={["/titles/7"]}>
          <SidebarProvider>
            <Routes>
              <Route path="/titles/:id" element={<TitleDetailPage />} />
            </Routes>
          </SidebarProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    expect(
      await screen.findByRole("heading", { level: 2, name: "Title not found" }),
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        (_, el) =>
          el?.tagName === "P" &&
          el.textContent ===
            "This title is no longer in your library. Back to titles.",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "Back to titles" }),
    ).toHaveAttribute("href", "/");
    expect(
      screen.queryByText(/Couldn’t load the title/),
    ).not.toBeInTheDocument();
  });
});
