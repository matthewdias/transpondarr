import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";
import type { Settings } from "@/lib/api";
import { IndexerSection } from "@/pages/settings/sections/indexer";

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

function settings(indexer: Settings["indexer"]): Settings {
  return {
    download: {
      configured: false,
      url: "",
      user: "",
      password_set: false,
      category: "",
    },
    indexer,
    library: { configured: false, dir: "", mode: "" },
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

function renderSection(indexer: Settings["indexer"]) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <IndexerSection settings={settings(indexer)} />
    </QueryClientProvider>,
  );
}

describe("IndexerSection", () => {
  // #142: the filter is stored config, not a secret, so it is seeded from the
  // snapshot and an edit — including clearing it — goes out with the save.
  it("seeds the category filter and saves an edited one", async () => {
    let body: unknown;
    server.use(
      http.put("/api/v1/settings/indexer", async ({ request }) => {
        body = await request.json();
        return HttpResponse.json(
          settings({
            configured: true,
            name: "prowlarr",
            url: "http://prowlarr:9696/1/api",
            apikey_set: true,
            categories: "5070",
          }),
        );
      }),
    );
    const user = userEvent.setup();
    renderSection({
      configured: true,
      name: "prowlarr",
      url: "http://prowlarr:9696/1/api",
      apikey_set: true,
      categories: "5070,127720",
    });

    const field = screen.getByLabelText(/categories/i);
    expect(field).toHaveValue("5070,127720");

    await user.clear(field);
    await user.type(field, "5070");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(body).toMatchObject({ categories: "5070" });
  });
});
