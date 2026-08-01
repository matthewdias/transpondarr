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

function settings(automation: Settings["automation"]): Settings {
  return {
    download: {
      configured: false,
      url: "",
      user: "",
      password_set: false,
      category: "",
    },
    indexer: { configured: false, name: "", url: "", apikey_set: false },
    library: { configured: false, dir: "", mode: "" },
    automation,
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
  // The service clamps the delay, so a save can return a different number than
  // was typed. The field must re-seed from the response — otherwise it keeps
  // showing the rejected value while the toast says "saved".
  it("re-seeds the delay field with the clamped value a save returns", async () => {
    server.use(
      http.put("/api/v1/settings/automation", () =>
        HttpResponse.json(settings({ enabled: true, pin_delay_hours: 8760 })),
      ),
    );
    const user = userEvent.setup();
    renderSection({ enabled: true, pin_delay_hours: 0 });

    const field = screen.getByRole("spinbutton");
    await user.clear(field);
    await user.type(field, "3000000");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByDisplayValue("8760")).toBe(field);
  });
});
