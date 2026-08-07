import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, expect, it } from "vitest";
import { api, ApiError, PartialBatchError } from "@/lib/api";

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

type Body = { item_ids: number[]; monitored: boolean };

// The endpoint caps a batch at 1000, so anything over that has to arrive as
// more than one request or the whole selection 422s and rolls back.
function capture(batches: Body[], failFrom?: number) {
  server.use(
    http.patch("/api/v1/wanted/items", async ({ request }) => {
      const body = (await request.json()) as Body;
      if (body.item_ids.length > 1000) {
        return HttpResponse.json({ detail: "too many items" }, { status: 422 });
      }
      batches.push(body);
      if (failFrom !== undefined && batches.length > failFrom) {
        return HttpResponse.json({ detail: "boom" }, { status: 500 });
      }
      return HttpResponse.json({
        updated: body.item_ids.length,
        series_queued: 1,
      });
    }),
  );
}

const ids = (n: number) => Array.from({ length: n }, (_, i) => i + 1);

it("splits a selection past the cap into sequential batches", async () => {
  const batches: Body[] = [];
  capture(batches);

  const res = await api.setItemsMonitored(ids(1202), false);

  expect(batches.map((b) => b.item_ids.length)).toEqual([1000, 202]);
  expect(batches.every((b) => b.monitored === false)).toBe(true);
  // Every id lands exactly once, so the count is exact even chunked.
  expect(res.updated).toBe(1202);
  // A series straddling a boundary would be counted twice, and the client
  // cannot dedupe a count -- so it reports "not derivable" rather than a lie.
  expect(res.series_queued).toBeNull();
});

it("passes the server's own count through when one batch suffices", async () => {
  const batches: Body[] = [];
  capture(batches);

  const res = await api.setItemsMonitored(ids(3), true);

  expect(batches).toHaveLength(1);
  expect(res.updated).toBe(3);
  expect(res.series_queued).toBe(1);
});

// The flag is idempotent, so retrying is safe -- but the user has to be told
// where it stopped rather than handed a bare failure.
it("reports how much landed when a later batch fails", async () => {
  const batches: Body[] = [];
  capture(batches, 1); // the second request onwards fails

  const err = await api
    .setItemsMonitored(ids(1202), false)
    .then(() => null)
    .catch((e: unknown) => e);

  expect(err).toBeInstanceOf(PartialBatchError);
  expect((err as PartialBatchError).applied).toBe(1000);
  expect((err as PartialBatchError).total).toBe(1202);
  expect((err as PartialBatchError).message).toMatch(/1000 of 1202/);
});

// Nothing landed, so this is an ordinary failure and must not claim otherwise.
it("throws the plain error when the first batch fails", async () => {
  const batches: Body[] = [];
  capture(batches, 0);

  await expect(api.setItemsMonitored(ids(5), false)).rejects.toBeInstanceOf(
    ApiError,
  );
});
