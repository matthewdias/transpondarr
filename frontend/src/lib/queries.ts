// Query key + fetch wiring for every server query, in one place. Call sites use
// useQuery(fooQuery(...)) and spread in call-site state like `enabled`; signal
// threading for cancellation lives here so components never mention it.
import {
  infiniteQueryOptions,
  keepPreviousData,
  queryOptions,
} from "@tanstack/react-query";
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

// Both wanted listings page by keyset, and both keys start with "wanted" so a
// queued search invalidates the pair with one prefix.
export const wantedMissingQuery = (unaired: boolean, unmonitored: boolean) =>
  infiniteQueryOptions({
    queryKey: ["wanted", "missing", unaired, unmonitored],
    queryFn: ({ pageParam, signal }) =>
      api.wantedMissing(pageParam, unaired, unmonitored, signal),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor || undefined,
  });

export const wantedCutoffQuery = (unmonitored: boolean) =>
  infiniteQueryOptions({
    queryKey: ["wanted", "cutoff", unmonitored],
    queryFn: ({ pageParam, signal }) =>
      api.wantedCutoffUnmet(pageParam, unmonitored, signal),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor || undefined,
  });

export const settingsQuery = () =>
  queryOptions({
    queryKey: ["settings"],
    queryFn: ({ signal }) => api.getSettings(signal),
  });

// Exported because it bounds how stale a rendered job snapshot can be, which is
// what the card's overdue threshold has to clear.
export const JOBS_POLL_MS = 15 * 1000;

// Job telemetry ages on its own with nothing to invalidate it, so this is the
// one query that polls: a wedged or erroring job should surface while the
// Settings page is open, not only on a reload.
export const jobsQuery = () =>
  queryOptions({
    queryKey: ["jobs"],
    queryFn: ({ signal }) => api.listJobs(signal),
    refetchInterval: JOBS_POLL_MS,
  });

// Polls alongside the job table: the breaker opens and closes on its own, so a
// Settings page left open should show the fault arriving, not a stale snapshot.
export const blocklistSummaryQuery = () =>
  queryOptions({
    queryKey: ["blocklist-summary"],
    queryFn: ({ signal }) => api.blocklistSummary(signal),
    refetchInterval: JOBS_POLL_MS,
  });

// Exported so tests and the page agree on how stale a queue snapshot can be.
export const ACTIVITY_QUEUE_POLL_MS = 15 * 1000;

// Polls like jobs: torrent progress ages on its own with nothing to invalidate it.
export const activityQueueQuery = () =>
  queryOptions({
    queryKey: ["activity-queue"],
    queryFn: ({ signal }) => api.activityQueue(signal),
    refetchInterval: ACTIVITY_QUEUE_POLL_MS,
  });

// Keyset pagination: each page carries the cursor for the next, absent on the last.
export const activityHistoryQuery = () =>
  infiniteQueryOptions({
    queryKey: ["activity-history"],
    queryFn: ({ pageParam, signal }) => api.activityHistory(pageParam, signal),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor || undefined,
  });

// Enabled by the dialog opening: the read walks the payload on disk, so it must
// not run for every deferred row merely listed in the queue.
export const queueItemPayloadQuery = (id: number) =>
  queryOptions({
    queryKey: ["queue-payload", id],
    queryFn: ({ signal }) => api.queueItemPayload(id, signal),
  });

export const profilesQuery = () =>
  queryOptions({
    queryKey: ["profiles"],
    queryFn: ({ signal }) => api.listProfiles(signal),
  });
