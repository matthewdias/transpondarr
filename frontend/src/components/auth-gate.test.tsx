import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, expect, it } from "vitest";
import { AuthGate } from "@/components/auth-gate";

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

function problem(status: number, detail: string) {
  return HttpResponse.json(
    { status, detail },
    { status, headers: { "Content-Type": "application/problem+json" } },
  );
}

function authStatus(configured: boolean) {
  return http.get("/api/v1/auth/status", () =>
    HttpResponse.json({
      configured,
      required: "enabled",
      authenticated: false,
      session: false,
      username: "",
      local: false,
    }),
  );
}

function renderGate() {
  render(
    <QueryClientProvider client={new QueryClient()}>
      <AuthGate>
        <p>Signed in</p>
      </AuthGate>
    </QueryClientProvider>,
  );
}

it("shows the server's reason when setup rejects the password", async () => {
  server.use(
    authStatus(false),
    http.post("/api/v1/auth/setup", () =>
      problem(400, "Use a password of at least 8 characters."),
    ),
  );
  renderGate();
  const user = userEvent.setup();
  await user.type(await screen.findByLabelText("Username"), "admin");
  await user.type(screen.getByLabelText("Password"), "short");
  await user.type(screen.getByLabelText("Confirm password"), "short");
  await user.click(screen.getByRole("button", { name: "Create account" }));
  expect(
    await screen.findByText("Use a password of at least 8 characters."),
  ).toBeInTheDocument();
});

it("shows why a login failed: rejected, rate-limited or unreachable", async () => {
  server.use(
    authStatus(true),
    http.post("/api/v1/auth/login", () =>
      problem(401, "Wrong username or password."),
    ),
  );
  renderGate();
  const user = userEvent.setup();
  await user.type(await screen.findByLabelText("Username"), "admin");
  await user.type(screen.getByLabelText("Password"), "wrongpass");
  await user.click(screen.getByRole("button", { name: "Sign in" }));
  expect(
    await screen.findByText("Wrong username or password."),
  ).toBeInTheDocument();

  server.use(
    http.post("/api/v1/auth/login", () =>
      problem(
        429,
        "Too many password attempts. Wait 15 minutes, then try again.",
      ),
    ),
  );
  await user.click(screen.getByRole("button", { name: "Sign in" }));
  expect(
    await screen.findByText(
      "Too many password attempts. Wait 15 minutes, then try again.",
    ),
  ).toBeInTheDocument();

  server.use(http.post("/api/v1/auth/login", () => HttpResponse.error()));
  await user.click(screen.getByRole("button", { name: "Sign in" }));
  expect(
    await screen.findByText(
      "Couldn’t reach Transpondarr. Check that the server is running, then try again.",
    ),
  ).toBeInTheDocument();
});
