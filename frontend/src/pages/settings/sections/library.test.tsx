import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";
import type { Settings } from "@/lib/api";
import { LibrarySection } from "@/pages/settings/sections/library";

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const notifyEvents = {
  on_grabbed: true,
  on_imported: true,
  on_stuck: true,
  on_grab_failed: true,
  on_title_added: true,
  on_rehearsal: true,
};

function settings(library: Settings["library"]): Settings {
  return {
    download: {
      configured: false,
      url: "",
      user: "",
      password_set: false,
      category: "",
      stall_hours: 6,
    },
    indexer: {
      configured: false,
      name: "",
      url: "",
      apikey_set: false,
      categories: "",
    },
    library,
    automation: { mode: "off", pin_delay_hours: 0 },
    notifications: {
      discord: { configured: false, url: "", ...notifyEvents },
      webhook: { configured: false, url: "", ...notifyEvents },
      ntfy: {
        configured: false,
        server: "https://ntfy.sh",
        topic: "",
        token_set: false,
        ...notifyEvents,
      },
    },
    auth: { configured: false, username: "", required: "enabled" },
    general: {
      version: "test",
      addr: "",
      api_key: "",
      data_dir: "",
      db_path: "",
    },
  };
}

function renderSection(library: Settings["library"]) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <LibrarySection settings={settings(library)} />
    </QueryClientProvider>,
  );
}

describe("LibrarySection", () => {
  // #198: movies place into their own root, so the section edits both and a
  // save carries them together.
  it("seeds both roots and saves an edited movies directory", async () => {
    let body: unknown;
    const saved: Settings["library"] = {
      configured: true,
      dir: "/media/Anime",
      movies_dir: "/media/Anime Films",
      series_layout: "season_folders",
      mode: "auto",
    };
    server.use(
      http.put("/api/v1/settings/library", async ({ request }) => {
        body = await request.json();
        return HttpResponse.json(settings(saved));
      }),
    );
    const user = userEvent.setup();
    renderSection({
      configured: true,
      dir: "/media/Anime",
      movies_dir: "",
      series_layout: "season_folders",
      mode: "auto",
    });

    expect(screen.getByLabelText(/^library directory/i)).toHaveValue(
      "/media/Anime",
    );
    const movies = screen.getByLabelText(/^movies directory/i);
    expect(movies).toHaveValue("");

    await user.type(movies, "/media/Anime Films");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(body).toMatchObject({
      dir: "/media/Anime",
      movies_dir: "/media/Anime Films",
      mode: "auto",
    });
  });

  // #129: the layout is edited here, and a save that does not touch it must
  // carry the season folders an existing library is already in.
  it("seeds the stored layout and saves a switch to flat", async () => {
    let body: unknown;
    server.use(
      http.put("/api/v1/settings/library", async ({ request }) => {
        body = await request.json();
        return HttpResponse.json(
          settings({
            configured: true,
            dir: "/media/Anime",
            movies_dir: "",
            series_layout: "flat",
            mode: "auto",
          }),
        );
      }),
    );
    const user = userEvent.setup();
    renderSection({
      configured: true,
      dir: "/media/Anime",
      movies_dir: "",
      series_layout: "season_folders",
      mode: "auto",
    });

    const layout = screen.getByLabelText(/^series layout/i);
    expect(layout).toHaveValue("season_folders");

    await user.selectOptions(layout, "flat");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(body).toMatchObject({ dir: "/media/Anime", series_layout: "flat" });
  });
});
