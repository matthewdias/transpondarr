import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";
import type { Settings } from "@/lib/api";
import { DownloadSection } from "@/pages/settings/sections/download";

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

function settings(download: Settings["download"]): Settings {
  return {
    download,
    indexer: {
      configured: false,
      name: "",
      url: "",
      apikey_set: false,
      categories: "",
    },
    library: {
      configured: false,
      dir: "",
      movies_dir: "",
      series_layout: "season_folders",
      mode: "",
    },
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

const stored: Settings["download"] = {
  configured: true,
  url: "http://qb:8080",
  user: "admin",
  password_set: true,
  category: "transpondarr",
  stall_hours: 6,
};

function renderSection(download: Settings["download"] = stored) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <DownloadSection settings={settings(download)} />
    </QueryClientProvider>,
  );
}

describe("DownloadSection", () => {
  // 0 is the deliberate "never give up", so it has to survive the round trip as
  // a number rather than being dropped as an empty field (#242).
  it("saves the stall timeout, zero included", async () => {
    let body: unknown;
    server.use(
      http.put("/api/v1/settings/download", async ({ request }) => {
        body = await request.json();
        return HttpResponse.json(settings({ ...stored, stall_hours: 0 }));
      }),
    );
    const user = userEvent.setup();
    renderSection();

    const field = screen.getByRole("spinbutton");
    expect(field).toHaveValue(6);
    await user.clear(field);
    await user.type(field, "0");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(body).toMatchObject({ stall_hours: 0 });
  });

  // The service clamps the hours, so a save can return a different number than
  // was typed; the field must re-seed from the response rather than keep showing
  // the rejected value under a "saved" toast (the automation precedent).
  it("re-seeds the field with the clamped value a save returns", async () => {
    server.use(
      http.put("/api/v1/settings/download", () =>
        HttpResponse.json(settings({ ...stored, stall_hours: 8760 })),
      ),
    );
    const user = userEvent.setup();
    renderSection();

    const field = screen.getByRole("spinbutton");
    await user.clear(field);
    await user.type(field, "3000000");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByDisplayValue("8760")).toBe(field);
  });
});
