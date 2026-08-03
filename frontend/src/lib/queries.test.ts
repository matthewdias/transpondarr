import { keepPreviousData } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import {
  ACTIVITY_QUEUE_POLL_MS,
  activityHistoryQuery,
  activityQueueQuery,
  authStatusQuery,
  browseSeasonQuery,
  grabsQuery,
  jobsQuery,
  metadataSearchQuery,
  releasesQuery,
  seriesDetailQuery,
  seriesQuery,
  settingsQuery,
} from "@/lib/queries";

describe("query key factories", () => {
  it("produce identical keys for identical arguments", () => {
    expect(seriesDetailQuery(7).queryKey).toEqual(
      seriesDetailQuery(7).queryKey,
    );
    expect(metadataSearchQuery("example").queryKey).toEqual(
      metadataSearchQuery("example").queryKey,
    );
  });

  it("scope parameterized keys by their argument", () => {
    expect(seriesDetailQuery(1).queryKey).not.toEqual(
      seriesDetailQuery(2).queryKey,
    );
    expect(
      browseSeasonQuery({ season: "summer", year: 2026 }).queryKey,
    ).not.toEqual(browseSeasonQuery({ season: "fall", year: 2026 }).queryKey);
    expect(metadataSearchQuery("a").queryKey).not.toEqual(
      metadataSearchQuery("b").queryKey,
    );
    expect(releasesQuery(1).queryKey).not.toEqual(releasesQuery(2).queryKey);
    expect(grabsQuery(1).queryKey).not.toEqual(grabsQuery(2).queryKey);
  });

  it("keeps the series list key a prefix of the detail key, so one invalidation covers both", () => {
    const list = seriesQuery().queryKey;
    const detail = seriesDetailQuery(42).queryKey;
    expect(detail.slice(0, list.length)).toEqual([...list]);
  });

  it("gives every resource a distinct key root", () => {
    const roots = [
      seriesQuery(),
      authStatusQuery(),
      metadataSearchQuery("x"),
      releasesQuery(1),
      grabsQuery(1),
      settingsQuery(),
      jobsQuery(),
      browseSeasonQuery({ season: "summer", year: 2026 }),
      activityQueueQuery(),
      activityHistoryQuery(),
    ].map((q) => q.queryKey[0]);
    expect(new Set(roots).size).toBe(roots.length);
  });

  it("polls the activity queue, since live client state goes stale on its own", () => {
    expect(activityQueueQuery().refetchInterval).toBe(ACTIVITY_QUEUE_POLL_MS);
    expect(ACTIVITY_QUEUE_POLL_MS).toBe(15 * 1000);
  });

  it("pages history by the server cursor and stops when it runs out", () => {
    const q = activityHistoryQuery();
    expect(q.initialPageParam).toBe("");
    const page = (next_cursor?: string) => ({
      events: [],
      ...(next_cursor ? { next_cursor } : {}),
    });
    expect(q.getNextPageParam(page("abc"), [page("abc")], "", [])).toBe("abc");
    expect(q.getNextPageParam(page(), [page()], "", [])).toBeUndefined();
  });

  it("polls job status, since it is telemetry that goes stale on its own", () => {
    expect(jobsQuery().refetchInterval).toBe(15 * 1000);
  });

  it("keeps releases fresh for five minutes to spare the rate-limited indexer", () => {
    expect(releasesQuery(3).staleTime).toBe(5 * 60 * 1000);
  });

  it("holds the outgoing chart on a season flip instead of flashing a skeleton", () => {
    expect(
      browseSeasonQuery({ season: "summer", year: 2026 }).placeholderData,
    ).toBe(keepPreviousData);
  });
});
