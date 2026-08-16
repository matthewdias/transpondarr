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

  it("maps problem+json failures to a thrown ApiError carrying the detail", async () => {
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

  // The 2026-08-15 AniList outage: detail was the handler's own summary and the
  // provider's explanation rode in errors[], where nothing read it.
  it("prefers the errors[] cause over the handler's detail", async () => {
    server.use(
      http.post("/api/v1/titles", () =>
        HttpResponse.json(
          {
            title: "Bad Gateway",
            detail: "failed to add series",
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
        "fetch metadata: anilist: status 403: The AniList API has been temporarily disabled due to severe stability issues.",
    });
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
            detail: "failed to add series",
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
      message: "failed to add series",
    });
  });

  it("falls back to detail when the body carries no errors[]", async () => {
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

  it("falls back to the status line when the body carries neither", async () => {
    server.use(
      http.get("/api/v1/titles", () => HttpResponse.json({}, { status: 503 })),
    );
    const err = await api.listTitles().catch((e: unknown) => e);
    expect(err).toMatchObject({ status: 503, message: "HTTP 503" });
  });

  // An upstream body reaches the toast verbatim, so a proxy's HTML page must not
  // fill it.
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
