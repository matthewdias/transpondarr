// Query key + fetch wiring for every server query, in one place. Call sites use
// useQuery(fooQuery(...)) and spread in call-site state like `enabled`; signal
// threading for cancellation lives here so components never mention it.
import { queryOptions } from "@tanstack/react-query";
import { api } from "@/lib/api";

// seriesQuery's key prefix-matches seriesDetailQuery's: invalidating it covers both.
export const seriesQuery = () =>
  queryOptions({
    queryKey: ["series"],
    queryFn: ({ signal }) => api.listSeries(signal),
  });

export const seriesDetailQuery = (id: number) =>
  queryOptions({
    queryKey: ["series", id],
    queryFn: ({ signal }) => api.getSeries(id, signal),
  });

export const authStatusQuery = () =>
  queryOptions({
    queryKey: ["auth-status"],
    queryFn: ({ signal }) => api.authStatus(signal),
  });

export const metadataSearchQuery = (term: string) =>
  queryOptions({
    queryKey: ["metadata-search", term],
    queryFn: ({ signal }) => api.searchMetadata(term, signal),
  });

// staleTime: Radix unmounts inactive tabs, so re-opening Releases would otherwise
// re-run a live, rate-limited indexer sweep; refetch() still forces a fresh search.
export const releasesQuery = (seriesId: number) =>
  queryOptions({
    queryKey: ["releases", seriesId],
    queryFn: ({ signal }) => api.searchReleases(seriesId, signal),
    staleTime: 5 * 60 * 1000,
  });

export const grabsQuery = (seriesId: number) =>
  queryOptions({
    queryKey: ["grabs", seriesId],
    queryFn: ({ signal }) => api.listGrabs(seriesId, signal),
  });

export const settingsQuery = () =>
  queryOptions({
    queryKey: ["settings"],
    queryFn: ({ signal }) => api.getSettings(signal),
  });
