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
  it("unwraps the title list envelope", async () => {
    const titles = [
      { id: 1, title: "Example Show", monitored: true },
      { id: 2, title: "Another Example", monitored: false },
    ];
    server.use(http.get("/api/v1/titles", () => HttpResponse.json({ titles })));
    await expect(api.listTitles()).resolves.toEqual(titles);
  });

  // The cross-origin check (#269) is middleware, not a Huma handler, so it writes
  // its own body. A text/plain one appears to the operator as "HTTP 403" with no
  // cause, which is what the changelog's upgrade note tells them to look for.
  it("surfaces the reason a cross-origin write was refused", async () => {
    server.use(
      http.get("/api/v1/titles", () =>
        HttpResponse.json(
          {
            title: "Forbidden",
            status: 403,
            detail: "cross-origin request refused",
          },
          {
            status: 403,
            headers: { "Content-Type": "application/problem+json" },
          },
        ),
      ),
    );
    const err = (await api.listTitles().catch((e: unknown) => e)) as ApiError;
    expect(err).toBeInstanceOf(ApiError);
    expect(err.message).toBe("cross-origin request refused");
  });

  it("maps problem+json failures to a thrown ApiError with the detail", async () => {
    server.use(
      http.get("/api/v1/titles", () =>
        HttpResponse.json(
          { title: "Internal Server Error", detail: "indexer unreachable" },
          { status: 500 },
        ),
      ),
    );
    const err = await api.listTitles().catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 500, message: "indexer unreachable" });
  });

  // The 2026-08-15 AniList outage: the provider's explanation was in errors[], and
  // the handler's summary alone did not say what went wrong.
  it("leads a 5xx with the handler's detail and keeps the errors[] cause", async () => {
    server.use(
      http.post("/api/v1/titles", () =>
        HttpResponse.json(
          {
            title: "Bad Gateway",
            detail: "Couldn't fetch the title from AniList.",
            errors: [
              {
                message:
                  "fetch metadata: anilist: status 403: The AniList API has been temporarily disabled due to severe stability issues.",
              },
            ],
          },
          { status: 502 },
        ),
      ),
    );
    const err = await api.addTitle("anilist", 1).catch((e: unknown) => e);
    expect(err).toMatchObject({
      status: 502,
      message:
        "Couldn't fetch the title from AniList. fetch metadata: anilist: status 403: The AniList API has been temporarily disabled due to severe stability issues.",
    });
  });

  it("shows the handler's message on a 500 whose errors[] has a cause", async () => {
    server.use(
      http.get("/api/v1/titles", () =>
        HttpResponse.json(
          {
            title: "Internal Server Error",
            detail: "Couldn't load the titles. The server log has the cause",
            errors: [{ message: "sql: database is closed" }],
          },
          { status: 500 },
        ),
      ),
    );
    const err = (await api.listTitles().catch((e: unknown) => e)) as ApiError;
    expect(err.message).toBe(
      "Couldn't load the titles. The server log has the cause. sql: database is closed",
    );
  });

  // Huma reuses errors[] for per-field validation, where one message misleads.
  it("joins every errors[] entry and names the field each came from", async () => {
    server.use(
      http.post("/api/v1/titles", () =>
        HttpResponse.json(
          {
            title: "Unprocessable Entity",
            detail: "validation failed",
            errors: [
              { message: "expected integer", location: "body.provider_id" },
              { message: "unexpected value", location: "body.provider" },
            ],
          },
          { status: 422 },
        ),
      ),
    );
    const err = await api.addTitle("anilist", 1).catch((e: unknown) => e);
    expect(err).toMatchObject({
      status: 422,
      message:
        "body.provider_id: expected integer; body.provider: unexpected value",
    });
  });

  // ProblemBody is an assertion over a body we did not write, not a check, so a
  // non-string message must not replace the ApiError with a TypeError.
  it("drops an errors[] entry whose message is not a string", async () => {
    server.use(
      http.post("/api/v1/titles", () =>
        HttpResponse.json(
          {
            title: "Bad Gateway",
            detail: "failed to add title",
            errors: [{ message: 404 }, { message: { nested: true } }],
          },
          { status: 502 },
        ),
      ),
    );
    const err = await api.addTitle("anilist", 1).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({
      status: 502,
      message: "failed to add title",
    });
  });

  it("falls back to detail when the body has no errors[]", async () => {
    server.use(
      http.get("/api/v1/titles", () =>
        HttpResponse.json(
          { title: "Internal Server Error", detail: "indexer unreachable" },
          { status: 500 },
        ),
      ),
    );
    const err = await api.listTitles().catch((e: unknown) => e);
    expect(err).toMatchObject({ status: 500, message: "indexer unreachable" });
  });

  it("falls back to the status line when the body has neither", async () => {
    server.use(
      http.get("/api/v1/titles", () => HttpResponse.json({}, { status: 503 })),
    );
    const err = await api.listTitles().catch((e: unknown) => e);
    expect(err).toMatchObject({ status: 503, message: "HTTP 503" });
  });

  // An upstream body appears in the toast verbatim, so a proxy's HTML page must
  // not fill it.
  it("bounds the composed message", async () => {
    server.use(
      http.get("/api/v1/titles", () =>
        HttpResponse.json(
          { errors: [{ message: "x".repeat(2048) }] },
          { status: 502 },
        ),
      ),
    );
    const err = (await api.listTitles().catch((e: unknown) => e)) as ApiError;
    expect(err.message.length).toBeLessThan(400);
    expect(err.message.endsWith("…")).toBe(true);
  });

  it("sends the pinned group and unwraps the echo", async () => {
    let sent: unknown;
    server.use(
      http.put("/api/v1/titles/7/pinned-group", async ({ request }) => {
        sent = await request.json();
        return HttpResponse.json({ title_id: 7, pinned_group: "ShinyRip" });
      }),
    );
    await expect(api.setTitlePinnedGroup(7, "ShinyRip")).resolves.toEqual({
      title_id: 7,
      pinned_group: "ShinyRip",
    });
    expect(sent).toEqual({ group: "ShinyRip" });
  });

  it("dispatches the auth-expired event on a stale-session 401", async () => {
    const listener = watchAuthExpired();
    server.use(
      http.get("/api/v1/titles", () => new HttpResponse(null, { status: 401 })),
    );
    await expect(api.listTitles()).rejects.toBeInstanceOf(UnauthorizedError);
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
