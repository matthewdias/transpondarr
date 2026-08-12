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

function renderForm(target: AddTitle, title: string) {
  const user = userEvent.setup();
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
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

const series: AddTitle = {
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
  const user = renderForm(series, "Placeholder Saga");

  expect(
    await screen.findByRole("combobox", { name: "Monitor" }),
  ).toHaveTextContent("All episodes");

  await user.click(
    screen.getByRole("button", { name: "Add Placeholder Saga" }),
  );

  await waitFor(() => expect(bodies).toHaveLength(1));
  expect(bodies[0]).toMatchObject({ monitor_items: "all" });
});
