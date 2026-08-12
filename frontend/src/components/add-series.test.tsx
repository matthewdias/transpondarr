import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter } from "react-router";
import { afterAll, afterEach, beforeAll, expect, it } from "vitest";
import { AddSeriesButton } from "@/components/add-series";

const server = setupServer(
  http.get("/api/v1/metadata/search", () =>
    HttpResponse.json({
      results: [
        {
          provider: "anilist",
          provider_id: 21,
          romaji: "Placeholder Saga",
          format: "TV",
          episodes: 1202,
          status: "RELEASING",
          year: 1999,
        },
      ],
    }),
  ),
  http.get("/api/v1/profiles", () =>
    HttpResponse.json({
      profiles: [
        { id: 1, name: "Default", is_default: true },
        { id: 2, name: "Sharper", is_default: false },
      ],
    }),
  ),
  // The add invalidates the series list, which this page never observes;
  // tolerate the request if something does.
  http.get("/api/v1/titles", () => HttpResponse.json({ titles: [] })),
);
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

// Records what the add actually sent, which is the only place the form's two
// choices become observable.
function captureAdd() {
  const bodies: Record<string, unknown>[] = [];
  server.use(
    http.post("/api/v1/titles", async ({ request }) => {
      bodies.push((await request.json()) as Record<string, unknown>);
      return HttpResponse.json(
        { id: 9, title: "Placeholder Saga", monitored: true, items: [] },
        { status: 201 },
      );
    }),
  );
  return bodies;
}

async function openWithResults() {
  const user = userEvent.setup();
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <AddSeriesButton />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  await user.click(screen.getByRole("button", { name: /add series/i }));
  await user.type(
    screen.getByPlaceholderText(/search anilist/i),
    "placeholder",
  );
  // The search box debounces by 350ms before the query fires.
  await screen.findByText("Placeholder Saga", undefined, { timeout: 3000 });
  return user;
}

// Every add-time decision now belongs to the title it is about, so the row
// button opens a form rather than adding on the spot -- and the search behind
// it survives, because picking the wrong title is the common misstep.
it("opens a per-title form and keeps the search behind it", async () => {
  const user = await openWithResults();

  expect(
    screen.queryByRole("combobox", { name: /monitor on add/i }),
  ).not.toBeInTheDocument();
  const row = screen.getByRole("button", { name: "Add Placeholder Saga" });
  expect(row).toHaveTextContent("Add");
  expect(screen.queryByText(/Add ·/)).not.toBeInTheDocument();

  await user.click(row);

  // A seasonal show is the common case, so the defaults are the whole form.
  expect(
    await screen.findByRole("combobox", { name: "Monitor" }),
  ).toHaveTextContent("All episodes");
  expect(
    screen.getByRole("combobox", { name: /quality profile/i }),
  ).toHaveTextContent("Default");
  expect(
    screen.queryByPlaceholderText(/search anilist/i),
  ).not.toBeInTheDocument();

  await user.click(screen.getByRole("button", { name: /back/i }));

  expect(screen.getByPlaceholderText(/search anilist/i)).toHaveValue(
    "placeholder",
  );
  expect(
    screen.getByRole("button", { name: "Add Placeholder Saga" }),
  ).toBeInTheDocument();
});

// A stacked dialog would have dismissed one layer at a time; the step it was
// replaced with owes the same, or Escape silently costs the typed search that
// Back preserves.
it("steps back from the form on Escape, and closes on the next one", async () => {
  const user = await openWithResults();

  await user.click(
    screen.getByRole("button", { name: "Add Placeholder Saga" }),
  );
  await screen.findByRole("combobox", { name: "Monitor" });

  await user.keyboard("{Escape}");

  expect(screen.getByPlaceholderText(/search anilist/i)).toHaveValue(
    "placeholder",
  );
  expect(
    screen.queryByRole("combobox", { name: "Monitor" }),
  ).not.toBeInTheDocument();

  await user.keyboard("{Escape}");
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
});

// Untouched is not a choice: an omitted profile has to leave the server's
// default alone, or every add would have to name one.
it("confirms with the defaults in one click and sends no profile", async () => {
  const bodies = captureAdd();
  const user = await openWithResults();

  await user.click(
    screen.getByRole("button", { name: "Add Placeholder Saga" }),
  );
  // The row button is gone; this is the form's submit.
  await user.click(
    await screen.findByRole("button", { name: "Add Placeholder Saga" }),
  );

  await waitFor(() => expect(bodies).toHaveLength(1));
  expect(bodies[0].monitor_items).toBe("all");
  expect(bodies[0]).not.toHaveProperty("quality_profile_id");
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
});

// The profile fetch must never stand between the user and the button: a failed
// one (or a slow one) degrades to the server's default, which is the same
// answer an untouched picker gives.
it("adds when the profiles cannot be fetched", async () => {
  const bodies = captureAdd();
  server.use(http.get("/api/v1/profiles", () => HttpResponse.error()));
  const user = await openWithResults();

  await user.click(
    screen.getByRole("button", { name: "Add Placeholder Saga" }),
  );
  const submit = await screen.findByRole("button", {
    name: "Add Placeholder Saga",
  });
  expect(
    screen.queryByRole("combobox", { name: /quality profile/i }),
  ).not.toBeInTheDocument();
  expect(submit).toBeEnabled();

  await user.click(submit);

  await waitFor(() => expect(bodies).toHaveLength(1));
  expect(bodies[0]).not.toHaveProperty("quality_profile_id");
});

it("sends both choices when they are made", async () => {
  const bodies = captureAdd();
  const user = await openWithResults();

  await user.click(
    screen.getByRole("button", { name: "Add Placeholder Saga" }),
  );

  await user.click(await screen.findByRole("combobox", { name: "Monitor" }));
  await user.click(screen.getByRole("option", { name: /future only/i }));
  await user.click(screen.getByRole("combobox", { name: /quality profile/i }));
  await user.click(screen.getByRole("option", { name: "Sharper" }));

  await user.click(
    screen.getByRole("button", { name: "Add Placeholder Saga" }),
  );

  await waitFor(() => expect(bodies).toHaveLength(1));
  expect(bodies[0]).toMatchObject({
    monitor_items: "future",
    quality_profile_id: 2,
  });
});

// "none" would store a cut that monitors nothing new forever, and nothing can
// edit the cut after the add. "Track it but grab nothing" is the series switch.
it("offers no way to add a series that monitors nothing", async () => {
  const user = await openWithResults();

  await user.click(
    screen.getByRole("button", { name: "Add Placeholder Saga" }),
  );
  await user.click(await screen.findByRole("combobox", { name: "Monitor" }));

  expect(screen.getAllByRole("option")).toHaveLength(2);
  expect(
    screen.queryByRole("option", { name: /^none/i }),
  ).not.toBeInTheDocument();
});
