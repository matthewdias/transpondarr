import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
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
import { MemoryRouter } from "react-router";
import { useQuery } from "@tanstack/react-query";
import type { SeriesDetail, WantedItem } from "@/lib/api";
import { seriesDetailQuery } from "@/lib/queries";
import { DeleteSeriesDialog } from "@/components/detail/delete-series-dialog";

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const item = (number: number, status: WantedItem["status"]): WantedItem => ({
  id: number,
  number,
  in_library: status === "in_library",
  monitored: true,
  status,
});

const detail: SeriesDetail = {
  id: 7,
  title: "Placeholder Saga",
  format: "TV",
  monitored: true,
  quality_profile_id: 1,
  items: [
    item(1, "in_library"),
    item(2, "in_library"),
    item(3, "downloading"),
    item(4, "deferred"),
    item(5, "stuck"),
    item(6, "wanted"),
  ],
};

function renderDialog(onDeleted = vi.fn()) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <DeleteSeriesDialog detail={detail} onDeleted={onDeleted} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return onDeleted;
}

describe("DeleteSeriesDialog", () => {
  it("says what goes and what stays, in counts", async () => {
    const user = userEvent.setup();
    renderDialog();

    await user.click(screen.getByRole("button", { name: /delete/i }));
    const dialog = screen.getByRole("dialog");
    // 6 tracked episodes go; the 2 in the library stay on disk.
    expect(dialog).toHaveTextContent(/6 tracked episodes/i);
    expect(dialog).toHaveTextContent(/2 episodes? in your library/i);
    // 3 active downloads (downloading + stuck + deferred) ride the checkbox label.
    expect(dialog).toHaveTextContent(/3 active downloads/i);
  });

  it("deletes without touching downloads by default", async () => {
    let url: URL | undefined;
    server.use(
      http.delete("/api/v1/series/7", ({ request }) => {
        url = new URL(request.url);
        return new HttpResponse(null, { status: 204 });
      }),
    );
    const user = userEvent.setup();
    const onDeleted = renderDialog();

    await user.click(screen.getByRole("button", { name: /delete/i }));
    expect(screen.getByRole("checkbox")).not.toBeChecked();
    await user.click(screen.getByRole("button", { name: /delete series/i }));

    await waitFor(() => expect(url).toBeDefined());
    expect(url?.searchParams.has("remove_downloads")).toBe(false);
    await waitFor(() => expect(onDeleted).toHaveBeenCalled());
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
  });

  it("sends remove_downloads=true when the box is checked", async () => {
    let url: URL | undefined;
    server.use(
      http.delete("/api/v1/series/7", ({ request }) => {
        url = new URL(request.url);
        return new HttpResponse(null, { status: 204 });
      }),
    );
    const user = userEvent.setup();
    renderDialog();

    await user.click(screen.getByRole("button", { name: /delete/i }));
    await user.click(screen.getByRole("checkbox"));
    await user.click(screen.getByRole("button", { name: /delete series/i }));

    await waitFor(() =>
      expect(url?.searchParams.get("remove_downloads")).toBe("true"),
    );
  });

  it("drops the mounted detail query instead of refetching it into a 404", async () => {
    let detailGets = 0;
    server.use(
      http.get("/api/v1/series/7", () => {
        detailGets++;
        return HttpResponse.json(detail);
      }),
      http.delete(
        "/api/v1/series/7",
        () => new HttpResponse(null, { status: 204 }),
      ),
    );
    // The dialog lives on the detail page, so its query is active when the
    // series-prefix invalidation lands.
    function Page() {
      useQuery(seriesDetailQuery(7));
      return <DeleteSeriesDialog detail={detail} onDeleted={() => {}} />;
    }
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    render(
      <QueryClientProvider client={client}>
        <MemoryRouter>
          <Page />
        </MemoryRouter>
      </QueryClientProvider>,
    );
    await waitFor(() => expect(detailGets).toBe(1));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /delete/i }));
    await user.click(screen.getByRole("button", { name: /delete series/i }));
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );

    expect(detailGets).toBe(1);
  });

  it("keeps the dialog open and reports a failed delete", async () => {
    server.use(
      http.delete("/api/v1/series/7", () =>
        HttpResponse.json(
          { title: "Bad Gateway", detail: "failed to remove downloads" },
          { status: 502 },
        ),
      ),
    );
    const user = userEvent.setup();
    const onDeleted = renderDialog();

    await user.click(screen.getByRole("button", { name: /delete/i }));
    await user.click(screen.getByRole("button", { name: /delete series/i }));

    // The dialog stays up for a retry; nothing navigates away.
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: /delete series/i }),
      ).toBeEnabled(),
    );
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(onDeleted).not.toHaveBeenCalled();
  });
});
