import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";
import type { NotificationsInput, Settings } from "@/lib/api";
import { NotificationsSection } from "@/pages/settings/sections/notifications";

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const offEvents = {
  on_grabbed: false,
  on_imported: false,
  on_stuck: false,
  on_grab_failed: false,
  on_title_added: false,
  on_rehearsal: false,
};

const onEvents = {
  on_grabbed: true,
  on_imported: true,
  on_stuck: true,
  on_grab_failed: true,
  on_title_added: true,
  on_rehearsal: true,
};

function notifications(
  over: Partial<Settings["notifications"]> = {},
): Settings["notifications"] {
  return {
    discord: { configured: false, url: "", ...onEvents },
    webhook: { configured: false, url: "", ...onEvents },
    ntfy: {
      configured: false,
      server: "https://ntfy.sh",
      topic: "",
      token_set: false,
      ...onEvents,
    },
    ...over,
  };
}

function settings(n: Settings["notifications"]): Settings {
  return {
    download: {
      configured: false,
      url: "",
      user: "",
      password_set: false,
      category: "",
    },
    indexer: {
      configured: false,
      name: "",
      url: "",
      apikey_set: false,
      categories: "",
    },
    library: { configured: false, dir: "", mode: "" },
    automation: { mode: "off", pin_delay_hours: 0 },
    notifications: n,
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

function renderSection(n: Settings["notifications"]) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <NotificationsSection settings={settings(n)} />
    </QueryClientProvider>,
  );
}

describe("NotificationsSection", () => {
  it("saves the whole section in one request, omitting a blank token", async () => {
    let body: NotificationsInput | undefined;
    server.use(
      http.put("/api/v1/settings/notifications", async ({ request }) => {
        body = (await request.json()) as NotificationsInput;
        return HttpResponse.json(settings(notifications()));
      }),
    );
    const user = userEvent.setup();
    renderSection(notifications());

    await user.type(
      screen.getByLabelText(/Discord webhook URL/),
      "https://discord.example/api/webhooks/1/abc",
    );
    await user.type(screen.getByLabelText(/ntfy topic/), "transpondarr");
    await user.click(screen.getByRole("switch", { name: "Discord Imported" }));
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(body).toBeDefined();
    expect(body?.discord.url).toBe(
      "https://discord.example/api/webhooks/1/abc",
    );
    expect(body?.discord.on_imported).toBe(false);
    expect(body?.discord.on_grabbed).toBe(true);
    expect(body?.webhook.on_stuck).toBe(true);
    expect(body?.ntfy.topic).toBe("transpondarr");
    expect(body?.ntfy && "token" in body.ntfy && body.ntfy.token).toBeFalsy();
  });

  it("tests each adapter against its own endpoint with its own result line", async () => {
    server.use(
      http.post("/api/v1/settings/notifications/discord/test", () =>
        HttpResponse.json({ status: "ok" }),
      ),
      http.post("/api/v1/settings/notifications/ntfy/test", () =>
        HttpResponse.json(
          { title: "Bad Gateway", detail: "notification test failed" },
          { status: 502 },
        ),
      ),
    );
    const user = userEvent.setup();
    renderSection(
      notifications({
        discord: { configured: true, url: "https://d.example/w", ...onEvents },
        ntfy: {
          configured: true,
          server: "https://ntfy.sh",
          topic: "transpondarr",
          token_set: false,
          ...offEvents,
        },
      }),
    );

    // Canonical adapter names: "Discord", "Webhook", "ntfy" (lowercase brand).
    expect(screen.getByRole("button", { name: "Test Webhook" })).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Test Discord" }));
    expect(await screen.findByText("Test notification sent.")).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Test ntfy" }));
    expect(await screen.findByText("notification test failed")).toBeTruthy();
    // The discord result line must survive the ntfy failure: state is per adapter.
    expect(screen.getByText("Test notification sent.")).toBeTruthy();
  });

  it("masks a stored ntfy token and hints that blank keeps it", () => {
    renderSection(
      notifications({
        ntfy: {
          configured: true,
          server: "https://ntfy.sh",
          topic: "transpondarr",
          token_set: true,
          ...onEvents,
        },
      }),
    );
    const token = screen.getByLabelText(/ntfy access token/);
    expect(token.getAttribute("placeholder")).toBe("•••••••• (unchanged)");
    expect(
      screen.getByText("Leave blank to keep the stored token."),
    ).toBeTruthy();
  });
});
