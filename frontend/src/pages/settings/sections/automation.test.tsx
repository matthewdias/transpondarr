import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";
import type { Settings } from "@/lib/api";
import { AutomationSection } from "@/pages/settings/sections/automation";

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

function settings(automation: Settings["automation"]): Settings {
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
    automation,
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

function renderSection(automation: Settings["automation"]) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <AutomationSection settings={settings(automation)} />
    </QueryClientProvider>,
  );
}

describe("AutomationSection", () => {
  // The hint describes the mode in the selector, so the default-state sentence
  // belongs to "Off" alone — on the other two it contradicts the setting.
  it("does not tell an enabled install that automation is off", async () => {
    const user = userEvent.setup();
    renderSection({ mode: "off", pin_delay_hours: 0 });
    expect(
      screen.getByText(/default until you turn automation on/i),
    ).toBeTruthy();

    await user.click(
      screen.getByRole("combobox", { name: "Automatic search and grab" }),
    );
    await user.click(await screen.findByRole("option", { name: "On" }));

    expect(screen.queryByText(/until you turn automation on/i)).toBeNull();
    expect(
      screen.getByText("Automation searches and grabs on its own."),
    ).toBeTruthy();
  });

  // #116: the third state rides the same section save as the old two.
  it("saves the mode picked in the selector", async () => {
    let body: unknown;
    server.use(
      http.put("/api/v1/settings/automation", async ({ request }) => {
        body = await request.json();
        return HttpResponse.json(
          settings({ mode: "notify_only", pin_delay_hours: 0 }),
        );
      }),
    );
    const user = userEvent.setup();
    renderSection({ mode: "off", pin_delay_hours: 0 });

    const mode = screen.getByRole("combobox", {
      name: "Automatic search and grab",
    });
    expect(mode).toHaveTextContent("Off");
    await user.click(mode);
    await user.click(
      await screen.findByRole("option", { name: "Notify only" }),
    );
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(body).toMatchObject({ mode: "notify_only" });
  });

  // The service clamps the delay, so a save can return a different number than
  // was typed. The field must re-seed from the response — otherwise it keeps
  // showing the rejected value while the toast says "saved".
  it("re-seeds the delay field with the clamped value a save returns", async () => {
    server.use(
      http.put("/api/v1/settings/automation", () =>
        HttpResponse.json(settings({ mode: "on", pin_delay_hours: 8760 })),
      ),
    );
    const user = userEvent.setup();
    renderSection({ mode: "on", pin_delay_hours: 0 });

    const field = screen.getByRole("spinbutton");
    await user.clear(field);
    await user.type(field, "3000000");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByDisplayValue("8760")).toBe(field);
  });
});
