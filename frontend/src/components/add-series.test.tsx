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
);
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

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

// The mode control sits above the result list, so the Add button is what has to
// carry it: flipping the Select rewrites every button, which is the only
// confirmation a distant control gives.
it("annotates the add button with the chosen mode", async () => {
  const user = await openWithResults();

  // "all" is the default and needs no qualifier.
  expect(
    screen.getByRole("button", { name: "Add Placeholder Saga" }),
  ).toHaveTextContent("Add");
  expect(screen.queryByText(/Add ·/)).not.toBeInTheDocument();

  await user.click(screen.getByRole("combobox", { name: /monitor on add/i }));
  await user.click(screen.getByRole("option", { name: /future only/i }));
  await waitFor(() =>
    expect(screen.getByText("Add · future only")).toBeInTheDocument(),
  );
});

// "none" would store a cut that monitors nothing new forever, and nothing can
// edit the cut after the add. "Track it but grab nothing" is the series switch.
it("offers no way to add a series that monitors nothing", async () => {
  const user = await openWithResults();

  await user.click(screen.getByRole("combobox", { name: /monitor on add/i }));
  expect(screen.getAllByRole("option")).toHaveLength(2);
  expect(
    screen.queryByRole("option", { name: /^none/i }),
  ).not.toBeInTheDocument();
});

// The visible label is identical on every row, so the accessible name is what
// distinguishes them -- and it must keep the annotation too.
it("keeps the candidate's title in the accessible name", async () => {
  const user = await openWithResults();

  await user.click(screen.getByRole("combobox", { name: /monitor on add/i }));
  await user.click(screen.getByRole("option", { name: /future only/i }));
  await waitFor(() =>
    expect(
      screen.getByRole("button", {
        name: "Add Placeholder Saga · future only",
      }),
    ).toBeInTheDocument(),
  );
});
