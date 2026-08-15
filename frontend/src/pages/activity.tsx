import {
  useInfiniteQuery,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import {
  Clock,
  Download,
  FileQuestion,
  FolderClock,
  History,
  Pause,
  RefreshCw,
  TriangleAlert,
  Wrench,
} from "lucide-react";
import { Link } from "react-router";
import { ApiError, type QueueItem, type UnmatchedDownload } from "@/lib/api";
import {
  ACTIVITY_QUEUE_POLL_MS,
  activityHistoryQuery,
  activityQueueQuery,
  activityUnmatchedQuery,
} from "@/lib/queries";
import {
  countdownOrDate,
  formatBytes,
  parseTimestamp,
  timeAgo,
} from "@/lib/format";
import { cn } from "@/lib/utils";
import { GrabEventRow } from "@/components/grab-event-row";
import { RemoveUnmatchedDialog } from "@/components/remove-unmatched-dialog";
import { RetryImportDialog } from "@/components/retry-import-dialog";
import { Topbar } from "@/components/topbar";
import { Button } from "@/components/ui/button";
import {
  Item,
  ItemActions,
  ItemContent,
  ItemGroup,
  ItemMedia,
} from "@/components/ui/item";
import { Skeleton } from "@/components/ui/skeleton";

export function ActivityPage() {
  return (
    <>
      <Topbar title="Activity" />
      <div className="space-y-8 px-4 py-6 sm:px-6">
        <QueueSection />
        <UnmatchedSection />
        <HistorySection />
      </div>
    </>
  );
}

function SectionSkeleton() {
  return (
    <div className="overflow-hidden rounded-lg border bg-card shadow-sm">
      {Array.from({ length: 2 }).map((_, i) => (
        <div
          key={i}
          className="flex items-center gap-3 border-b px-3.5 py-3 last:border-b-0"
        >
          <Skeleton className="size-8 rounded-lg" />
          <div className="flex-1 space-y-1.5">
            <Skeleton className="h-3.5 w-40" />
            <Skeleton className="h-3 w-56" />
          </div>
        </div>
      ))}
    </div>
  );
}

function SectionError({
  what,
  error,
  onRetry,
}: {
  what: string;
  error: unknown;
  onRetry: () => void;
}) {
  return (
    <div className="flex items-center gap-3 rounded-lg border border-dashed bg-card px-3.5 py-3">
      <TriangleAlert className="size-4 shrink-0 text-dl" />
      <p className="min-w-0 flex-1 text-[13px] text-muted-foreground">
        Couldn’t load {what}.{" "}
        {error instanceof ApiError ? error.message : String(error)}
      </p>
      <Button variant="outline" size="sm" onClick={onRetry}>
        <RefreshCw className="size-4" /> Try again
      </Button>
    </div>
  );
}

function QueueSection() {
  const { data, isLoading, isPaused, isError, error, refetch } =
    useQuery(activityQueueQuery());

  // History only changes when an item settles out of the open set (or shifts
  // derived status), so that is the invalidation signal — not progress ticks.
  const queryClient = useQueryClient();
  const openSet = data?.items.map((i) => `${i.id}:${i.status}`).join(",");
  const prevOpenSet = useRef<string>(undefined);
  useEffect(() => {
    if (openSet === undefined) return;
    if (prevOpenSet.current !== undefined && prevOpenSet.current !== openSet) {
      void queryClient.invalidateQueries({
        queryKey: activityHistoryQuery().queryKey,
      });
    }
    prevOpenSet.current = openSet;
  }, [openSet, queryClient]);

  return (
    <section>
      <h2 className="mb-2 text-sm font-semibold">Queue</h2>
      {isLoading || isPaused ? (
        <SectionSkeleton />
      ) : isError ? (
        <SectionError what="the queue" error={error} onRetry={refetch} />
      ) : !data || data.items.length === 0 ? (
        <div className="flex flex-col items-center rounded-lg border border-dashed bg-card py-10 text-center">
          <Download className="mb-3 size-7 text-faint" />
          <p className="text-sm text-muted-foreground">Nothing downloading.</p>
        </div>
      ) : (
        <>
          {!data.client_ok && (
            <p className="mb-2 text-[13px] text-muted-foreground">
              Download client unreachable — showing grab state only.
            </p>
          )}
          <ItemGroup className="overflow-hidden rounded-lg border bg-card shadow-sm [&>*+*]:border-t">
            {data.items.map((item) => (
              <QueueRow key={item.id} item={item} />
            ))}
          </ItemGroup>
        </>
      )}
    </section>
  );
}

// Live state outranks the derived status for the icon: a paused or stalled
// torrent is the thing worth noticing, whatever the pipeline calls the item.
function queueTone(item: QueueItem) {
  if (item.client_state === "paused")
    return { icon: Pause, tone: "bg-panel-2 text-muted-foreground" };
  // Waiting its turn is the client's own decision, so nothing is wrong with it
  // and nothing is happening to it (#246).
  if (item.client_state === "queued")
    return { icon: Clock, tone: "bg-panel-2 text-muted-foreground" };
  // data_missing is alarming on purpose: we decline to blame the release for it
  // (#241), which leaves the user as the only one who can act on it.
  if (
    item.client_state === "stalled" ||
    item.client_state === "error" ||
    item.client_state === "data_missing"
  )
    return { icon: TriangleAlert, tone: "bg-destructive/15 text-destructive" };
  if (item.status === "stuck")
    return { icon: TriangleAlert, tone: "bg-destructive/15 text-destructive" };
  if (item.status === "deferred")
    return { icon: FolderClock, tone: "bg-panel-2 text-muted-foreground" };
  return { icon: Download, tone: "bg-dl-weak text-dl" };
}

const clientStateLabel: Record<string, string> = {
  downloading: "Downloading",
  queued: "Queued",
  complete: "Complete",
  stalled: "Stalled",
  checking: "Checking",
  paused: "Paused",
  error: "Error",
  data_missing: "Data missing",
  unknown: "Unknown",
};

// The deadline can pass between import scans, where a countdown would read "in
// 0m" and an absolute date would read like a plan rather than an imminent one.
// Past countdownOrDate's own week-long cliff it renders a bare date, which needs
// the preposition a countdown does not.
function abandonLabel(at: string): string {
  const secs = (parseTimestamp(at) - Date.now()) / 1000;
  if (secs <= 0) return "giving up shortly";
  return `giving up ${secs >= 7 * 86400 ? "on " : ""}${countdownOrDate(at)}`;
}

function QueueRow({ item }: { item: QueueItem }) {
  const { icon: Icon, tone } = queueTone(item);
  const [fixing, setFixing] = useState(false);
  return (
    <Item className="gap-3">
      <ItemMedia>
        <span className={cn("grid size-8 place-items-center rounded-lg", tone)}>
          <Icon className="size-4" />
        </span>
      </ItemMedia>
      <ItemContent className="min-w-0 gap-0.5">
        <div className="text-sm font-medium">
          {/* A film's number says nothing its title has not, and calling it an
              episode is a plain false statement. Format alone decides (#208). */}
          {item.format !== "MOVIE" && (
            <>
              Episode {item.item_number}
              {" · "}
            </>
          )}
          <Link
            to={`/titles/${item.title_id}`}
            className="font-normal text-muted-foreground hover:text-foreground hover:underline"
          >
            {item.title}
          </Link>
        </div>
        <div className="line-clamp-1 font-mono text-[12px] text-faint">
          {item.release_title}
        </div>
        {item.import_error && (
          <div className="line-clamp-2 text-[12px] text-destructive">
            {item.import_error}
          </div>
        )}
      </ItemContent>
      <ItemActions className="flex-col items-end gap-1">
        {item.status === "deferred" && (
          <>
            <Button variant="outline" size="sm" onClick={() => setFixing(true)}>
              <Wrench className="size-4" /> Fix import
            </Button>
            <RetryImportDialog
              item={item}
              open={fixing}
              onOpenChange={setFixing}
            />
          </>
        )}
        {item.client_state && (
          <span className="text-xs font-medium">
            {clientStateLabel[item.client_state] ?? item.client_state}
            {item.progress !== undefined && (
              <span className="text-faint">
                {` · ${Math.round(item.progress * 100)}%`}
              </span>
            )}
            {item.abandon_at && (
              <span className="text-faint">
                {` · ${abandonLabel(item.abandon_at)}`}
              </span>
            )}
          </span>
        )}
        <span className="text-xs text-faint">{timeAgo(item.created_at)}</span>
      </ItemActions>
    </Item>
  );
}

// Unlike the other two sections this one vanishes when empty: an orphaned
// download is a rare state, and a permanent empty card would be noise on every
// visit. A load failure still speaks, since silence would be indistinguishable
// from "nothing is wrong".
function UnmatchedSection() {
  // The poll is paused while a confirm dialog is open: a scan or a new grab
  // adopting the hash would otherwise drop the row, unmounting the dialog the
  // user was reading with no explanation.
  const [confirming, setConfirming] = useState<string | null>(null);
  const { data, isError, error, refetch } = useQuery({
    ...activityUnmatchedQuery(),
    refetchInterval: confirming ? false : ACTIVITY_QUEUE_POLL_MS,
  });
  if (!isError && (!data || data.items.length === 0)) return null;

  return (
    <section>
      <h2 className="mb-2 text-sm font-semibold">Unmatched downloads</h2>
      {isError ? (
        <SectionError
          what="unmatched downloads"
          error={error}
          onRetry={refetch}
        />
      ) : (
        <>
          <p className="mb-2 text-[13px] text-muted-foreground">
            In Transpondarr’s category, but no episode is waiting on them —
            downloads a later grab replaced, and downloads kept when their
            series was deleted. Removing one is up to you.
          </p>
          <ItemGroup className="overflow-hidden rounded-lg border bg-card shadow-sm [&>*+*]:border-t">
            {data?.items.map((item) => (
              <UnmatchedRow
                key={item.infohash}
                item={item}
                onConfirming={(open) =>
                  setConfirming(open ? item.infohash : null)
                }
              />
            ))}
          </ItemGroup>
        </>
      )}
    </section>
  );
}

function UnmatchedRow({
  item,
  onConfirming,
}: {
  item: UnmatchedDownload;
  onConfirming: (open: boolean) => void;
}) {
  return (
    <Item className="gap-3">
      <ItemMedia>
        <span className="grid size-8 place-items-center rounded-lg bg-panel-2 text-muted-foreground">
          <FileQuestion className="size-4" />
        </span>
      </ItemMedia>
      <ItemContent className="min-w-0 gap-0.5">
        <div className="line-clamp-1 font-mono text-[13px]">{item.name}</div>
        <div className="text-[12px] text-faint">
          <span className="font-mono">{item.infohash}</span>
          {item.size > 0 && <> · {formatBytes(item.size)}</>}
          {item.added_at && <> · added {timeAgo(item.added_at)}</>}
        </div>
      </ItemContent>
      <ItemActions className="flex-col items-end gap-1">
        <RemoveUnmatchedDialog item={item} onOpenChange={onConfirming} />
        <span className="text-xs font-medium">
          {clientStateLabel[item.client_state] ?? item.client_state}
          <span className="text-faint">
            {` · ${Math.round(item.progress * 100)}%`}
          </span>
        </span>
      </ItemActions>
    </Item>
  );
}

function HistorySection() {
  const {
    data,
    isLoading,
    isPaused,
    isError,
    error,
    refetch,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
  } = useInfiniteQuery(activityHistoryQuery());

  const events = data?.pages.flatMap((p) => p.events) ?? [];

  return (
    <section>
      <h2 className="mb-2 text-sm font-semibold">History</h2>
      {isLoading || isPaused ? (
        <SectionSkeleton />
      ) : isError ? (
        <SectionError what="history" error={error} onRetry={refetch} />
      ) : events.length === 0 ? (
        <div className="flex flex-col items-center rounded-lg border border-dashed bg-card py-10 text-center">
          <History className="mb-3 size-7 text-faint" />
          <p className="text-sm text-muted-foreground">
            No grab or import history yet.
          </p>
        </div>
      ) : (
        <>
          <ItemGroup className="overflow-hidden rounded-lg border bg-card shadow-sm [&>*+*]:border-t">
            {events.map((e) => (
              <GrabEventRow
                key={e.id}
                event={e}
                title={{ id: e.title_id, name: e.title }}
              />
            ))}
          </ItemGroup>
          {hasNextPage && (
            <Button
              variant="outline"
              size="sm"
              className="mt-3"
              disabled={isFetchingNextPage}
              onClick={() => fetchNextPage()}
            >
              {isFetchingNextPage ? "Loading…" : "Load more"}
            </Button>
          )}
        </>
      )}
    </section>
  );
}
