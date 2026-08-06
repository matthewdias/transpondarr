// Typed client for the Transpondarr REST API. The embedded SPA is served at `/`;
// every `/api/*` call (except health and the auth status/setup/login endpoints)
// is authorized by the browser's httpOnly session cookie, sent automatically via
// `credentials: 'same-origin'`. No API key is ever held in JS — the key is for
// machine clients (`X-Api-Key`) only.
//
// Request/response shapes are generated from the backend's OpenAPI 3.1 spec into
// `api-types.ts` (run `make gen-api` after changing a Huma DTO). The in-spec
// endpoints go through an `openapi-fetch` client keyed on the generated `paths`,
// so response types are *derived* from the spec and can't silently drift from the
// backend. The six auth endpoints are plain chi handlers outside the spec, so
// they use `rawFetch` — same credentials and error mapping, hand-declared shapes.

import createClient from "openapi-fetch";
import type { components, paths } from "./api-types";

/** Dispatched on window when a request 401s, so the auth gate can re-open. */
export const AUTH_EXPIRED_EVENT = "transpondarr:auth-expired";

/** Thrown for any non-2xx response; `status` lets callers special-case 401. */
export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

/** Raised specifically on 401 so callers can special-case bad/missing auth. */
export class UnauthorizedError extends ApiError {
  constructor(message = "Invalid credentials") {
    super(401, message);
    this.name = "UnauthorizedError";
  }
}

// Central failure handling shared by the typed client (unwrap) and the auth calls
// (rawFetch). A 401 normally means the session went stale, so it re-opens the
// auth gate even for calls outside React Query — but for endpoints whose job is
// to *check* credentials (login, setup, change password), a 401 is just a wrong
// password: those pass authEvent: false so the gate doesn't remount and wipe the
// form before its error can render.
function throwApiError(status: number, body: unknown, authEvent = true): never {
  if (status === 401) {
    if (authEvent) window.dispatchEvent(new Event(AUTH_EXPIRED_EVENT));
    throw new UnauthorizedError();
  }
  const problem = body as { detail?: string; title?: string } | null;
  throw new ApiError(
    status,
    problem?.detail || problem?.title || `HTTP ${status}`,
  );
}

// Browser auth rides the httpOnly session cookie (same-origin); openapi-fetch
// sets Content-Type: application/json for requests with a body.
const client = createClient<paths>({ credentials: "same-origin" });

// openapi-fetch resolves to { data, error, response } and never throws; unwrap
// restores throw-on-error semantics (React Query queryFns/mutationFns rely on a
// thrown error) and routes failures through the shared handler.
function unwrap<T>(res: { data?: T; error?: unknown; response: Response }): T {
  if (res.response.ok) return res.data as T;
  throwApiError(res.response.status, res.error);
}

// The auth endpoints set and read the session cookie directly and aren't in the
// OpenAPI spec, so they can't go through the typed client. rawFetch mirrors its
// behavior for those calls. Auth handlers return plain-text errors (not
// problem+json), so a failed JSON parse just falls back to the status line.
async function rawFetch<T>(
  path: string,
  init: RequestInit = {},
  { authEvent = true } = {},
): Promise<T> {
  const headers = new Headers(init.headers);
  if (init.body) headers.set("Content-Type", "application/json");
  const res = await fetch(path, {
    ...init,
    headers,
    credentials: "same-origin",
  });
  if (!res.ok) {
    let body: unknown = null;
    try {
      body = await res.json();
    } catch {
      // non-JSON body — keep the status line
    }
    throwApiError(res.status, body, authEvent);
  }
  if (res.status === 204) return undefined as T;
  return res.json() as Promise<T>;
}

// ── Domain types (generated from the OpenAPI spec; see api-types.ts) ───────────

type Schemas = components["schemas"];

export type Series = Schemas["SeriesDTO"];
export type Candidate = Schemas["CandidateDTO"];
// The id space a provider_id is numbered in; the spec's enum, so a new provider
// widens this type rather than passing an arbitrary string.
export type Provider = Schemas["AddSeriesInputBody"]["provider"];
export type SeasonEntry = Schemas["SeasonEntryDTO"];
export type SeasonChart = Schemas["BrowseSeasonOutputBody"];
export type WantedItem = Schemas["DetailItemDTO"];
export type ItemStatus = WantedItem["status"]; // 'in_library' | 'downloading' | 'stuck' | 'deferred' | 'wanted'
export type CandidateRelease = Schemas["CandidateReleaseDTO"];
export type CalendarItem = Schemas["CalendarItemDTO"];
export type UnscheduledSeries = Schemas["UnscheduledSeriesDTO"];
export type Calendar = Schemas["CalendarOutputBody"];
export type GrabEvent = Schemas["GrabEventDTO"];
export type MissingItem = Schemas["MissingItemDTO"];
export type MissingGroup = Schemas["MissingGroupDTO"];
export type SeriesMissingReason = MissingGroup["reason"];
export type ItemMissingReason = NonNullable<MissingItem["reason"]>;
export type GlobalMissingReason = NonNullable<
  Schemas["MissingOutputBody"]["global_reason"]
>;
export type CutoffItem = Schemas["CutoffItemDTO"];
export type CutoffGroup = Schemas["CutoffGroupDTO"];
export type BlocklistEntry = Schemas["BlocklistEntryDTO"];
export type BlocklistSummary = Schemas["BlocklistSummaryOutputBody"];
export type GrabResult = Schemas["GrabSeriesOutputBody"];
export type DownloadSettings = Schemas["DownloadSettingsDTO"];
export type IndexerSettings = Schemas["IndexerSettingsDTO"];
export type LibrarySettings = Schemas["LibrarySettingsDTO"];
export type GeneralSettings = Schemas["GeneralSettingsDTO"];
export type AuthSettings = Schemas["AuthSettingsDTO"];
export type AutomationSettings = Schemas["AutomationSettingsDTO"];
export type NotificationsSettings = Schemas["NotificationsSettingsDTO"];
export type Settings = Schemas["SettingsDTO"];
export type AutomationMode = Settings["automation"]["mode"];
export type DownloadInput = Schemas["DownloadInputBody"];
export type IndexerInput = Schemas["IndexerInputBody"];
export type LibraryInput = Schemas["LibraryInputBody"];
export type AutomationInput = Schemas["AutomationInputBody"];
export type NotificationsInput = Schemas["NotificationsInputBody"];
export type JobStatus = Schemas["JobStatusDTO"];
export type QueueItem = Schemas["QueueItemDTO"];
export type ActivityEvent = Schemas["ActivityEventDTO"];
export type ActivityQueue = Schemas["ActivityQueueOutputBody"];
export type ActivityHistoryPage = Schemas["ActivityHistoryOutputBody"];
export type QueuePayload = Schemas["QueuePayloadOutputBody"];
export type PayloadFile = Schemas["PayloadFileDTO"];
export type PayloadArchive = Schemas["PayloadArchiveDTO"];
export type RetryAssignment = Schemas["RetryAssignmentDTO"];
export type RetryResult = Schemas["RetryResultDTO"];

// The read model for a single series with its wanted items. Arrays are
// non-nullable in the OpenAPI schema (Huma's DefaultArrayNullable is off; the
// backend always emits []), so this is a plain alias — no null remapping needed.
export type SeriesDetail = Schemas["SeriesDetailReadDTO"];
export type QualityProfile = Schemas["QualityProfileDTO"];
export type ProfileGroup = Schemas["ProfileGroupDTO"];
export type ProfileInput = Schemas["ProfileBody"];

// The auth status/setup/login endpoints are plain chi handlers (not Huma), so they
// are not in the OpenAPI spec — these shapes stay hand-declared.
export interface AuthStatus {
  configured: boolean;
  required: string;
  authenticated: boolean;
  /** True only when a real login session backs the request — not the API key or
   * the local-address bypass. Lets the UI hide "Sign out" when there's no session
   * to end (e.g. a loopback client in `local` required-mode). */
  session: boolean;
  username: string;
  local: boolean;
}

// ── Endpoints ─────────────────────────────────────────────────────────────────

// Mutations deliberately take no AbortSignal: a grab or a settings write must
// not die on a stray unmount.
export const api = {
  health: (signal?: AbortSignal) =>
    client.GET("/api/v1/health", { signal }).then(unwrap),

  // ── Auth (plain chi, not in the OpenAPI spec → rawFetch) ─────────────────────
  authStatus: (signal?: AbortSignal) =>
    rawFetch<AuthStatus>("/api/v1/auth/status", { signal }),

  setup: (username: string, password: string) =>
    rawFetch<{ username: string }>(
      "/api/v1/auth/setup",
      { method: "POST", body: JSON.stringify({ username, password }) },
      { authEvent: false },
    ),

  login: (username: string, password: string) =>
    rawFetch<{ username: string }>(
      "/api/v1/auth/login",
      { method: "POST", body: JSON.stringify({ username, password }) },
      { authEvent: false },
    ),

  logout: () => rawFetch<void>("/api/v1/auth/logout", { method: "POST" }),

  changePassword: (currentPassword: string, newPassword: string) =>
    rawFetch<void>(
      "/api/v1/auth/password",
      {
        method: "POST",
        body: JSON.stringify({
          current_password: currentPassword,
          new_password: newPassword,
        }),
      },
      { authEvent: false },
    ),

  setAuthMode: (required: string) =>
    rawFetch<{ required: string }>("/api/v1/auth/mode", {
      method: "POST",
      body: JSON.stringify({ required }),
    }),

  // ── In-spec endpoints (typed client) ────────────────────────────────────────
  regenerateApiKey: () =>
    client
      .POST("/api/v1/settings/apikey/regenerate")
      .then(unwrap)
      .then((r) => r.api_key),

  listSeries: (signal?: AbortSignal) =>
    client
      .GET("/api/v1/series", { signal })
      .then(unwrap)
      .then((r) => r.series),

  getSeries: (id: number, signal?: AbortSignal) =>
    client
      .GET("/api/v1/series/{id}", { params: { path: { id } }, signal })
      .then(unwrap),

  // remove_downloads rides as `true` or not at all, so the default request
  // carries no param.
  deleteSeries: (id: number, removeDownloads?: boolean) =>
    client
      .DELETE("/api/v1/series/{id}", {
        params: {
          path: { id },
          query: { remove_downloads: removeDownloads || undefined },
        },
      })
      .then(unwrap),

  setMonitored: (id: number, monitored: boolean) =>
    client
      .PATCH("/api/v1/series/{id}", {
        params: { path: { id } },
        body: { monitored },
      })
      .then(unwrap),

  browseSeason: (
    season: "winter" | "spring" | "summer" | "fall",
    year: number,
    signal?: AbortSignal,
  ) =>
    client
      .GET("/api/v1/browse/season", {
        params: { query: { season, year } },
        signal,
      })
      .then(unwrap),

  calendar: (
    start: string,
    end: string,
    unmonitored = false,
    signal?: AbortSignal,
  ) =>
    client
      .GET("/api/v1/calendar", {
        params: { query: { start, end, unmonitored } },
        signal,
      })
      .then(unwrap),

  wantedMissing: (
    cursor: string,
    unaired: boolean,
    unmonitored: boolean,
    signal?: AbortSignal,
  ) =>
    client
      .GET("/api/v1/wanted/missing", {
        params: {
          query: { ...(cursor ? { cursor } : {}), unaired, unmonitored },
        },
        signal,
      })
      .then(unwrap),

  wantedCutoffUnmet: (
    cursor: string,
    unmonitored: boolean,
    signal?: AbortSignal,
  ) =>
    client
      .GET("/api/v1/wanted/cutoff-unmet", {
        params: { query: { ...(cursor ? { cursor } : {}), unmonitored } },
        signal,
      })
      .then(unwrap),

  queueWantedSearch: (seriesIds: number[]) =>
    client
      .POST("/api/v1/wanted/search", { body: { series_ids: seriesIds } })
      .then(unwrap),

  searchMetadata: (term: string, signal?: AbortSignal) =>
    client
      .GET("/api/v1/metadata/search", { params: { query: { term } }, signal })
      .then(unwrap)
      .then((r) => r.results),

  addSeries: (provider: Provider, providerId: number, monitored = true) =>
    client
      .POST("/api/v1/series", {
        body: { provider, provider_id: providerId, monitored },
      })
      .then(unwrap),

  searchReleases: (id: number, signal?: AbortSignal) =>
    client
      .GET("/api/v1/series/{id}/search", { params: { path: { id } }, signal })
      .then(unwrap),

  grabRelease: (id: number, downloadUrl: string, paused = false) =>
    client
      .POST("/api/v1/series/{id}/grab", {
        params: { path: { id } },
        body: { download_url: downloadUrl, paused },
      })
      .then(unwrap),

  activityQueue: (signal?: AbortSignal) =>
    client.GET("/api/v1/activity/queue", { signal }).then(unwrap),

  activityHistory: (cursor: string, signal?: AbortSignal) =>
    client
      .GET("/api/v1/activity/history", {
        params: { query: cursor ? { cursor } : {} },
        signal,
      })
      .then(unwrap),

  queueItemPayload: (id: number, signal?: AbortSignal) =>
    client
      .GET("/api/v1/activity/queue/{id}/payload", {
        params: { path: { id } },
        signal,
      })
      .then(unwrap),

  retryQueueItemImport: (id: number, assignments: RetryAssignment[]) =>
    client
      .POST("/api/v1/activity/queue/{id}/retry-import", {
        params: { path: { id } },
        body: { assignments },
      })
      .then(unwrap)
      .then((r) => r.results),

  listGrabs: (id: number, signal?: AbortSignal) =>
    client
      .GET("/api/v1/series/{id}/grabs", { params: { path: { id } }, signal })
      .then(unwrap)
      .then((r) => r.events),

  listBlocklist: (id: number, signal?: AbortSignal) =>
    client
      .GET("/api/v1/series/{id}/blocklist", {
        params: { path: { id } },
        signal,
      })
      .then(unwrap)
      .then((r) => r.entries),

  blocklistSummary: (signal?: AbortSignal) =>
    client.GET("/api/v1/blocklist", { signal }).then(unwrap),

  clearBlocklist: () =>
    client
      .DELETE("/api/v1/blocklist", {})
      .then(unwrap)
      .then((r) => r.cleared),

  clearSeriesBlocklist: (id: number, expiredOnly = false) =>
    client
      .DELETE("/api/v1/series/{id}/blocklist", {
        params: {
          path: { id },
          ...(expiredOnly ? { query: { expired: true } } : {}),
        },
      })
      .then(unwrap)
      .then((r) => r.cleared),

  clearBlocklistEntry: (id: number, entryId: number) =>
    client
      .DELETE("/api/v1/series/{id}/blocklist/{entryId}", {
        params: { path: { id, entryId } },
      })
      .then(unwrap),

  getSettings: (signal?: AbortSignal) =>
    client.GET("/api/v1/settings", { signal }).then(unwrap),

  updateDownload: (body: DownloadInput) =>
    client.PUT("/api/v1/settings/download", { body }).then(unwrap),
  testDownload: (body: DownloadInput) =>
    client.POST("/api/v1/settings/download/test", { body }).then(unwrap),

  updateIndexer: (body: IndexerInput) =>
    client.PUT("/api/v1/settings/indexer", { body }).then(unwrap),
  testIndexer: (body: IndexerInput) =>
    client.POST("/api/v1/settings/indexer/test", { body }).then(unwrap),

  updateLibrary: (body: LibraryInput) =>
    client.PUT("/api/v1/settings/library", { body }).then(unwrap),
  testLibrary: (body: LibraryInput) =>
    client.POST("/api/v1/settings/library/test", { body }).then(unwrap),

  updateAutomation: (body: AutomationInput) =>
    client.PUT("/api/v1/settings/automation", { body }).then(unwrap),

  updateNotifications: (body: NotificationsInput) =>
    client.PUT("/api/v1/settings/notifications", { body }).then(unwrap),
  testNotifyDiscord: (body: NotificationsInput) =>
    client
      .POST("/api/v1/settings/notifications/discord/test", { body })
      .then(unwrap),
  testNotifyWebhook: (body: NotificationsInput) =>
    client
      .POST("/api/v1/settings/notifications/webhook/test", { body })
      .then(unwrap),
  testNotifyNtfy: (body: NotificationsInput) =>
    client
      .POST("/api/v1/settings/notifications/ntfy/test", { body })
      .then(unwrap),

  listJobs: (signal?: AbortSignal) =>
    client
      .GET("/api/v1/system/jobs", { signal })
      .then(unwrap)
      .then((r) => r.jobs),

  runJob: (name: string) =>
    client
      .POST("/api/v1/system/jobs/{name}/run", { params: { path: { name } } })
      .then(unwrap),

  listProfiles: (signal?: AbortSignal) =>
    client
      .GET("/api/v1/profiles", { signal })
      .then(unwrap)
      .then((r) => r.profiles),

  createProfile: (body: ProfileInput) =>
    client.POST("/api/v1/profiles", { body }).then(unwrap),

  updateProfile: (id: number, body: ProfileInput) =>
    client
      .PUT("/api/v1/profiles/{id}", { params: { path: { id } }, body })
      .then(unwrap),

  deleteProfile: (id: number, reassignTo?: number) =>
    client
      .DELETE("/api/v1/profiles/{id}", {
        params: { path: { id }, query: { reassign_to: reassignTo } },
      })
      .then(unwrap),

  assignSeriesProfile: (seriesId: number, profileId: number) =>
    client
      .PUT("/api/v1/series/{id}/profile", {
        params: { path: { id: seriesId } },
        body: { profile_id: profileId },
      })
      .then(unwrap),

  // delayHours is PUT-replace: omitting it drops back to the global default.
  setSeriesPinnedGroup: (
    seriesId: number,
    group: string,
    delayHours?: number,
  ) =>
    client
      .PUT("/api/v1/series/{id}/pinned-group", {
        params: { path: { id: seriesId } },
        body:
          delayHours === undefined
            ? { group }
            : { group, delay_hours: delayHours },
      })
      .then(unwrap),
};
