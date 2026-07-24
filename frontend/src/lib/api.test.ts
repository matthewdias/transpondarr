// MSW intercepts at the fetch layer so openapi-fetch and rawFetch run for real —
// the frontend analog of internal/coretest: fake the boundary, keep the rest real.
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import {
  afterAll,
  afterEach,
  beforeAll,
  describe,
  expect,
  it,
  vi,
} from "vitest";
import {
  api,
  ApiError,
  AUTH_EXPIRED_EVENT,
  UnauthorizedError,
} from "@/lib/api";

const server = setupServer();

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const listeners: Array<() => void> = [];
function watchAuthExpired() {
  const listener = vi.fn();
  window.addEventListener(AUTH_EXPIRED_EVENT, listener);
  listeners.push(listener);
  return listener;
}
afterEach(() => {
  for (const l of listeners.splice(0))
    window.removeEventListener(AUTH_EXPIRED_EVENT, l);
});

describe("typed client (openapi-fetch)", () => {
  it("unwraps the series list envelope", async () => {
    const series = [
      { id: 1, title: "Example Show", monitored: true },
      { id: 2, title: "Another Example", monitored: false },
    ];
    server.use(http.get("/api/v1/series", () => HttpResponse.json({ series })));
    await expect(api.listSeries()).resolves.toEqual(series);
  });

  it("maps problem+json failures to a thrown ApiError carrying the detail", async () => {
    server.use(
      http.get("/api/v1/series", () =>
        HttpResponse.json(
          { title: "Internal Server Error", detail: "indexer unreachable" },
          { status: 500 },
        ),
      ),
    );
    const err = await api.listSeries().catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 500, message: "indexer unreachable" });
  });

  it("dispatches the auth-expired event on a stale-session 401", async () => {
    const listener = watchAuthExpired();
    server.use(
      http.get("/api/v1/series", () => new HttpResponse(null, { status: 401 })),
    );
    await expect(api.listSeries()).rejects.toBeInstanceOf(UnauthorizedError);
    expect(listener).toHaveBeenCalledTimes(1);
  });
});

describe("auth endpoints (rawFetch)", () => {
  it("parses the auth status body", async () => {
    server.use(
      http.get("/api/v1/auth/status", () =>
        HttpResponse.json({
          configured: true,
          required: "always",
          authenticated: true,
          session: true,
          username: "example-user",
          local: false,
        }),
      ),
    );
    await expect(api.authStatus()).resolves.toMatchObject({
      authenticated: true,
      username: "example-user",
    });
  });

  it("keeps a failed login quiet so the form can render its own error", async () => {
    const listener = watchAuthExpired();
    server.use(
      http.post(
        "/api/v1/auth/login",
        () => new HttpResponse("invalid credentials", { status: 401 }),
      ),
    );
    await expect(api.login("example-user", "wrong")).rejects.toBeInstanceOf(
      UnauthorizedError,
    );
    expect(listener).not.toHaveBeenCalled();
  });
});
