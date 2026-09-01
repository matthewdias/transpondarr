import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useQuery } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";
import { titleDetailQuery } from "@/lib/queries";
import { SetEpisodeCountDialog } from "@/components/detail/set-episode-count-dialog";

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

function renderDialog() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <SetEpisodeCountDialog titleId={7} />
    </QueryClientProvider>,
  );
  return userEvent.setup();
}

describe("SetEpisodeCountDialog", () => {
  it("posts the typed count and closes", async () => {
    let body: unknown;
    server.use(
      http.post("/api/v1/titles/7/items", async ({ request }) => {
        body = await request.json();
        return HttpResponse.json({ created: 12 }, { status: 201 });
      }),
    );
    const user = renderDialog();

    await user.click(
      screen.getByRole("button", { name: /set episode count/i }),
    );
    await user.type(screen.getByRole("spinbutton"), "12");
    await user.click(screen.getByRole("button", { name: /^create/i }));

    await waitFor(() => expect(body).toEqual({ count: 12 }));
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
  });

  // The count is never prefilled from a release search: maxItem is the bound
  // decide uses to distrust release numbers, so a release must not set it.
  it("opens with an empty field and refuses to submit one", async () => {
    const user = renderDialog();

    await user.click(
      screen.getByRole("button", { name: /set episode count/i }),
    );
    expect(screen.getByRole("spinbutton")).toHaveValue(null);
    expect(screen.getByRole("button", { name: /^create/i })).toBeDisabled();
  });

  it("refetches the title after creating its items", async () => {
    let detailGets = 0;
    server.use(
      http.get("/api/v1/titles/7", () => {
        detailGets++;
        return HttpResponse.json({
          id: 7,
          title: "Placeholder Saga",
          format: "TV",
          monitored: true,
          quality_profile_id: 1,
          items: [],
        });
      }),
      http.post("/api/v1/titles/7/items", () =>
        HttpResponse.json({ created: 12 }, { status: 201 }),
      ),
    );
    // An active observer, because invalidation only refetches what is mounted.
    function Page() {
      useQuery(titleDetailQuery(7));
      return <SetEpisodeCountDialog titleId={7} />;
    }
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    render(
      <QueryClientProvider client={client}>
        <Page />
      </QueryClientProvider>,
    );
    await waitFor(() => expect(detailGets).toBe(1));

    const user = userEvent.setup();
    await user.click(
      screen.getByRole("button", { name: /set episode count/i }),
    );
    await user.type(screen.getByRole("spinbutton"), "12");
    await user.click(screen.getByRole("button", { name: /^create/i }));

    await waitFor(() => expect(detailGets).toBe(2));
  });

  it("keeps the dialog open and reports a refusal", async () => {
    server.use(
      http.post("/api/v1/titles/7/items", () =>
        HttpResponse.json(
          { title: "Conflict", detail: "series already has wanted items" },
          { status: 409 },
        ),
      ),
    );
    const user = renderDialog();

    await user.click(
      screen.getByRole("button", { name: /set episode count/i }),
    );
    await user.type(screen.getByRole("spinbutton"), "12");
    await user.click(screen.getByRole("button", { name: /^create/i }));

    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^create/i })).toBeEnabled(),
    );
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });
});
