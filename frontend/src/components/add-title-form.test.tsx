import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter } from "react-router";
import { afterAll, afterEach, beforeAll, expect, it } from "vitest";
import { type AddTitle, AddTitleForm } from "@/components/add-title-form";

const server = setupServer(
  http.get("/api/v1/profiles", () =>
    HttpResponse.json({
      profiles: [{ id: 1, name: "Default", is_default: true }],
    }),
  ),
  http.get("/api/v1/titles", () => HttpResponse.json({ titles: [] })),
  http.get("/api/v1/settings", () =>
    HttpResponse.json({
      automation: { mode: "on" },
      library: {
        dir: "/media/shows",
        movies_dir: "/media/films",
        mode: "hardlink",
        configured: true,
      },
    }),
  ),
);
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

function captureAdd() {
  const bodies: Record<string, unknown>[] = [];
  server.use(
    http.post("/api/v1/titles", async ({ request }) => {
      bodies.push((await request.json()) as Record<string, unknown>);
      return HttpResponse.json(
        { id: 9, title: "Sample Film", monitored: true, items: [{ id: 1 }] },
        { status: 201 },
      );
    }),
  );
  return bodies;
}

function settingsBody(dir: string, moviesDir: string) {
  return {
    automation: { mode: "on" },
    library: {
      dir,
      movies_dir: moviesDir,
      mode: "hardlink",
      configured: true,
    },
  };
}

// seedMoviesDir warms the ["settings"] cache the way any detail-page visit does
// (title-detail fetches it unconditionally). That is what makes the isMovie
// guard observable: enabled:false suppresses the fetch, never the cache read, so
// without the guard a series form would render the notice on its first tick.
function renderForm(target: AddTitle, title: string, seedMoviesDir?: string) {
  const user = userEvent.setup();
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  if (seedMoviesDir !== undefined) {
    client.setQueryData(
      ["settings"],
      settingsBody("/media/shows", seedMoviesDir),
    );
  }
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <AddTitleForm title={title} target={target} onAdded={() => {}} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return user;
}

const movie: AddTitle = {
  provider: "anilist",
  provider_id: 4321,
  format: "MOVIE",
  episodes: 1,
  status: "FINISHED",
};

const title: AddTitle = {
  provider: "anilist",
  provider_id: 21,
  format: "TV",
  episodes: 12,
  status: "RELEASING",
};

// All vs. future is meaningless for a single-item run: the add-time choice
// presents as monitored yes/no, which the series-level switch already is.
it("hides the monitor mode for a movie and sends all", async () => {
  const bodies = captureAdd();
  const user = renderForm(movie, "Sample Film");

  await screen.findByRole("combobox", { name: /quality profile/i });
  expect(
    screen.queryByRole("combobox", { name: "Monitor" }),
  ).not.toBeInTheDocument();

  await user.click(screen.getByRole("button", { name: "Add Sample Film" }));

  await waitFor(() => expect(bodies).toHaveLength(1));
  expect(bodies[0]).toMatchObject({ monitor_items: "all" });
});

// The mode keys on the format alone, so a series is untouched.
it("keeps the monitor mode for a series", async () => {
  const bodies = captureAdd();
  const user = renderForm(title, "Placeholder Saga");

  expect(
    await screen.findByRole("combobox", { name: "Monitor" }),
  ).toHaveTextContent("All episodes");

  await user.click(
    screen.getByRole("button", { name: "Add Placeholder Saga" }),
  );

  await waitFor(() => expect(bodies).toHaveLength(1));
  expect(bodies[0]).toMatchObject({ monitor_items: "all" });
});

function libraryRoots(dir: string, moviesDir: string) {
  server.use(
    http.get("/api/v1/settings", () =>
      HttpResponse.json(settingsBody(dir, moviesDir)),
    ),
  );
}

const noMoviesRoot = /movies (directory|folder|library)|Settings/i;

// #198 made a missing movies root a configuration error at import time; the add
// form is where a user can find that out before it bites them.
it("warns when a film is added with no movies root configured", async () => {
  libraryRoots("/media/shows", "");
  const bodies = captureAdd();
  const user = renderForm(movie, "Sample Film");

  const note = await screen.findByRole("link", { name: noMoviesRoot });
  expect(note).toHaveAttribute("href", "/settings");

  // Never blocking: gating a manual path is what #198 and PR #57 both refuse.
  await user.click(screen.getByRole("button", { name: "Add Sample Film" }));
  await waitFor(() => expect(bodies).toHaveLength(1));
});

// Either root alone is a library, so a films-only install is not misconfigured.
it("stays quiet on a movies-only library", async () => {
  libraryRoots("", "/media/films");
  renderForm(movie, "Sample Film");

  await screen.findByRole("combobox", { name: /quality profile/i });
  expect(screen.queryByRole("link", { name: noMoviesRoot })).toBeNull();
});

// The root a series places into is not this film-only concern. Seeded rather
// than fetched so the answer is present on the first render: with the settings
// only ever arriving async, this assertion would run before they landed and pass
// however the component behaved.
it("stays quiet for a series even with the empty root already cached", async () => {
  libraryRoots("/media/shows", "");
  renderForm(title, "Placeholder Saga", "");

  await screen.findByRole("combobox", { name: "Monitor" });
  expect(screen.queryByRole("link", { name: noMoviesRoot })).toBeNull();
});

// The same seeded cache on a film does show it, which is what proves the test
// above is testing the guard rather than the timing.
it("shows the notice from that same cached root when the title is a film", async () => {
  libraryRoots("/media/shows", "");
  renderForm(movie, "Sample Film", "");

  expect(
    await screen.findByRole("link", { name: noMoviesRoot }),
  ).toBeInTheDocument();
});
