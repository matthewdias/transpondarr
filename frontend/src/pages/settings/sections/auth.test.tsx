import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, expect, it, vi } from "vitest";
import { AUTH_EXPIRED_EVENT, type Settings } from "@/lib/api";
import { AuthSection } from "./auth";

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const settings = {
  auth: { configured: true, required: "enabled", username: "admin" },
} as Settings;

it("shows the server's reason a password change was refused, without signing out", async () => {
  const expired = vi.fn();
  window.addEventListener(AUTH_EXPIRED_EVENT, expired);
  server.use(
    http.post("/api/v1/auth/password", () =>
      HttpResponse.json(
        { status: 401, detail: "Your current password is wrong." },
        { status: 401 },
      ),
    ),
  );
  render(
    <QueryClientProvider client={new QueryClient()}>
      <AuthSection settings={settings} />
    </QueryClientProvider>,
  );
  const user = userEvent.setup();
  await user.type(screen.getByPlaceholderText("Current password"), "wrong");
  await user.type(screen.getByPlaceholderText("New password"), "brandnew1");
  await user.type(
    screen.getByPlaceholderText("Confirm new password"),
    "brandnew1",
  );
  await user.click(screen.getByRole("button", { name: "Update password" }));
  expect(
    await screen.findByText("Your current password is wrong."),
  ).toBeInTheDocument();
  expect(expired).not.toHaveBeenCalled();
  window.removeEventListener(AUTH_EXPIRED_EVENT, expired);
});
