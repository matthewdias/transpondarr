import {
  useInfiniteQuery,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import {
  Download,
  FolderClock,
  History,
  Pause,
  RefreshCw,
  TriangleAlert,
  Wrench,
} from "lucide-react";
import { Link } from "react-router";
import { ApiError, type QueueItem } from "@/lib/api";
import { activityHistoryQuery, activityQueueQuery } from "@/lib/queries";
import { timeAgo } from "@/lib/format";
import { cn } from "@/lib/utils";
import { GrabEventRow } from "@/components/grab-event-row";
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
  if (item.client_state === "stalled" || item.client_state === "error")
    return { icon: TriangleAlert, tone: "bg-destructive/15 text-destructive" };
  if (item.status === "stuck")
    return { icon: TriangleAlert, tone: "bg-destructive/15 text-destructive" };
  if (item.status === "deferred")
    return { icon: FolderClock, tone: "bg-panel-2 text-muted-foreground" };
  return { icon: Download, tone: "bg-dl-weak text-dl" };
}

const clientStateLabel: Record<string, string> = {
  downloading: "Downloading",
  complete: "Complete",
  stalled: "Stalled",
  checking: "Checking",
  paused: "Paused",
  error: "Error",
  unknown: "Unknown",
};

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
          Episode {item.item_number}
          {" · "}
          <Link
            to={`/series/${item.series_id}`}
            className="font-normal text-muted-foreground hover:text-foreground hover:underline"
          >
            {item.series_title}
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
          </span>
        )}
        <span className="text-xs text-faint">{timeAgo(item.created_at)}</span>
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
                series={{ id: e.series_id, title: e.series_title }}
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
