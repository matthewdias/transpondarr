import { describe, expect, it } from "vitest";
import {
  authStatusQuery,
  grabsQuery,
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
    ].map((q) => q.queryKey[0]);
    expect(new Set(roots).size).toBe(roots.length);
  });

  it("keeps releases fresh for five minutes to spare the rate-limited indexer", () => {
    expect(releasesQuery(3).staleTime).toBe(5 * 60 * 1000);
  });
});
