// Query key + fetch wiring for every server query, in one place. Call sites use
// useQuery(fooQuery(...)) and spread in call-site state like `enabled`; signal
// threading for cancellation lives here so components never mention it.
import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { api } from "@/lib/api";
import type { SeasonRef } from "@/lib/season";

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

// Blocklist entries outlive grab rows, so this is separate data, not a slice of
// the grab feed.
export const blocklistQuery = (seriesId: number) =>
  queryOptions({
    queryKey: ["blocklist", seriesId],
    queryFn: ({ signal }) => api.listBlocklist(seriesId, signal),
  });

// staleTime: the chart is a 6h-TTL cache server-side; refetching on every
// season flip would only re-read the same snapshot. keepPreviousData holds the
// outgoing chart on a flip instead of flashing the skeleton.
export const browseSeasonQuery = ({ season, year }: SeasonRef) =>
  queryOptions({
    queryKey: ["browse-season", season, year],
    queryFn: ({ signal }) => api.browseSeason(season, year, signal),
    staleTime: 5 * 60 * 1000,
    placeholderData: keepPreviousData,
  });

// keepPreviousData holds the outgoing grid while a prev/next page loads
// instead of flashing it empty.
export const calendarQuery = (
  start: string,
  end: string,
  unmonitored = false,
) =>
  queryOptions({
    queryKey: ["calendar", start, end, unmonitored],
    queryFn: ({ signal }) => api.calendar(start, end, unmonitored, signal),
    placeholderData: keepPreviousData,
  });

export const settingsQuery = () =>
  queryOptions({
    queryKey: ["settings"],
    queryFn: ({ signal }) => api.getSettings(signal),
  });

// Job telemetry ages on its own with nothing to invalidate it, so this is the
// one query that polls: a wedged or erroring job should surface while the
// Settings page is open, not only on a reload.
export const jobsQuery = () =>
  queryOptions({
    queryKey: ["jobs"],
    queryFn: ({ signal }) => api.listJobs(signal),
    refetchInterval: 15 * 1000,
  });

export const profilesQuery = () =>
  queryOptions({
    queryKey: ["profiles"],
    queryFn: ({ signal }) => api.listProfiles(signal),
  });
