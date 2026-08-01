import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Ban,
  Check,
  ChevronDown,
  ChevronRight,
  CircleX,
  Download,
  FolderClock,
  History,
  RefreshCw,
  TriangleAlert,
} from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { ApiError, api, type BlocklistEntry, type GrabEvent } from "@/lib/api";
import { blocklistQuery, grabsQuery } from "@/lib/queries";
import { countdownOrDate, timeAgo } from "@/lib/format";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import {
  Item,
  ItemContent,
  ItemMedia,
  ItemActions,
  ItemGroup,
} from "@/components/ui/item";
import { Skeleton } from "@/components/ui/skeleton";

function present(event: GrabEvent) {
  switch (event.status) {
    case "imported":
      return { verb: "Imported", icon: Check, tone: "bg-have-weak text-have" };
    case "import_deferred":
      return {
        verb: "Downloaded (batch)",
        icon: FolderClock,
        tone: "bg-panel-2 text-muted-foreground",
      };
    case "failed":
      return {
        verb: "Failed",
        icon: CircleX,
        tone: "bg-destructive/15 text-destructive",
      };
    default:
      return event.last_error
        ? {
            verb: "Import blocked",
            icon: TriangleAlert,
            tone: "bg-destructive/15 text-destructive",
          }
        : { verb: "Downloading", icon: Download, tone: "bg-dl-weak text-dl" };
  }
}

export function HistoryTab({
  seriesId,
  active,
}: {
  seriesId: number;
  active: boolean;
}) {
  const {
    data: events,
    isLoading,
    isPaused,
    isError,
    error,
    refetch,
  } = useQuery({
    ...grabsQuery(seriesId),
    enabled: active,
  });

  // A paused retry (browser offline) reports neither fetching nor error.
  if (isLoading || isPaused) {
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

  if (isError) {
    return (
      <div className="flex flex-col items-center rounded-lg border border-dashed bg-card px-6 py-14 text-center">
        <TriangleAlert className="mb-3 size-7 text-dl" />
        <h3 className="text-sm font-semibold">Couldn’t load history</h3>
        <p className="mt-1.5 max-w-md text-sm text-muted-foreground">
          {error instanceof ApiError ? error.message : String(error)}
        </p>
        <Button
          variant="outline"
          size="sm"
          className="mt-4"
          onClick={() => refetch()}
        >
          <RefreshCw className="size-4" /> Try again
        </Button>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {!events || events.length === 0 ? (
        <div className="flex flex-col items-center rounded-lg border border-dashed bg-card py-16 text-center">
          <History className="mb-3 size-7 text-faint" />
          <p className="text-sm text-muted-foreground">
            No grab or import history yet. Grab a release from the Releases tab.
          </p>
        </div>
      ) : (
        <ItemGroup className="overflow-hidden rounded-lg border bg-card shadow-sm [&>*+*]:border-t">
          {events.map((e) => (
            <HistoryRow key={e.id} event={e} />
          ))}
        </ItemGroup>
      )}
      <BlockedReleases seriesId={seriesId} active={active} />
    </div>
  );
}

// Blocklist entries outlive grab rows, so this is its own section and the feed's
// empty state must not swallow it.
export function BlockedReleases({
  seriesId,
  active,
}: {
  seriesId: number;
  active: boolean;
}) {
  const {
    data: entries,
    isError,
    error,
    refetch,
  } = useQuery({
    ...blocklistQuery(seriesId),
    enabled: active,
  });
  // null until the user decides, so the default can depend on data the first
  // render does not have yet.
  const [showExpired, setShowExpired] = useState<boolean | null>(null);

  if (isError) {
    return (
      <section>
        <h3 className="mb-2 text-sm font-semibold">Blocked releases</h3>
        <div className="flex items-center gap-3 rounded-lg border border-dashed bg-card px-3.5 py-3">
          <TriangleAlert className="size-4 shrink-0 text-dl" />
          <p className="min-w-0 flex-1 text-[13px] text-muted-foreground">
            Couldn’t load blocked releases.{" "}
            {error instanceof ApiError ? error.message : String(error)}
          </p>
          <Button variant="outline" size="sm" onClick={() => refetch()}>
            <RefreshCw className="size-4" /> Try again
          </Button>
        </div>
      </section>
    );
  }
  if (!entries || entries.length === 0) return null;

  const blocking = entries.filter((e) => e.active);
  const expired = entries.filter((e) => !e.active);
  // Expanded by default only when there is nothing else in the section to read.
  const expandExpired = showExpired ?? blocking.length === 0;

  return (
    <section>
      <h3 className="mb-2 text-sm font-semibold">Blocked releases</h3>
      {blocking.length > 0 && (
        <>
          <p className="mb-3 text-[13px] text-muted-foreground">
            Releases that failed and are skipped when ranking. Each repeat
            failure blocks for longer; the third blocks permanently.
          </p>
          <BlockedList seriesId={seriesId} entries={blocking} />
        </>
      )}
      {expired.length > 0 && (
        <>
          <Button
            variant="ghost"
            size="sm"
            className={cn(
              "text-muted-foreground",
              blocking.length > 0 && "mt-2",
            )}
            aria-expanded={expandExpired}
            onClick={() => setShowExpired(!expandExpired)}
          >
            {expandExpired ? (
              <ChevronDown className="size-4" />
            ) : (
              <ChevronRight className="size-4" />
            )}
            {expired.length} expired {expired.length === 1 ? "block" : "blocks"}
          </Button>
          {expandExpired && (
            <div className="mt-2">
              <p className="mb-3 text-[13px] text-muted-foreground">
                No longer skipped when ranking. Kept as history — a re-grab
                overwrites the failed grab row, and the failure count still
                escalates if the release fails again.
              </p>
              <BlockedList seriesId={seriesId} entries={expired} />
            </div>
          )}
        </>
      )}
    </section>
  );
}

function BlockedList({
  seriesId,
  entries,
}: {
  seriesId: number;
  entries: BlocklistEntry[];
}) {
  return (
    <ItemGroup className="overflow-hidden rounded-lg border bg-card shadow-sm [&>*+*]:border-t">
      {entries.map((e) => (
        <BlockedRow key={e.id} seriesId={seriesId} entry={e} />
      ))}
    </ItemGroup>
  );
}

function BlockedRow({
  seriesId,
  entry,
}: {
  seriesId: number;
  entry: BlocklistEntry;
}) {
  const queryClient = useQueryClient();
  const unblock = useMutation({
    mutationFn: () => api.clearBlocklistEntry(seriesId, entry.id),
    onSuccess: () => {
      toast.success("Release unblocked");
      queryClient.invalidateQueries({
        queryKey: blocklistQuery(seriesId).queryKey,
      });
      queryClient.invalidateQueries({
        queryKey: grabsQuery(seriesId).queryKey,
      });
    },
    onError: (e) =>
      toast.error("Could not unblock the release", {
        description: e instanceof Error ? e.message : String(e),
      }),
  });

  return (
    <Item className="gap-3">
      <ItemMedia>
        <span
          className={cn(
            "grid size-8 place-items-center rounded-lg",
            entry.active
              ? "bg-destructive/15 text-destructive"
              : "bg-panel-2 text-muted-foreground",
          )}
        >
          <Ban className="size-4" />
        </span>
      </ItemMedia>
      <ItemContent className="min-w-0 gap-0.5">
        <div className="line-clamp-1 font-mono text-[12px]">
          {entry.release_title}
        </div>
        <div className="text-[12px] text-muted-foreground">
          {entry.reason}
          {entry.failures > 1 && ` · ${entry.failures} failures`}
        </div>
        <div className="text-[12px] text-faint">{blockWindow(entry)}</div>
      </ItemContent>
      <ItemActions>
        <Button
          variant="outline"
          size="sm"
          disabled={unblock.isPending}
          onClick={() => unblock.mutate()}
        >
          Unblock
        </Button>
      </ItemActions>
    </Item>
  );
}

// "Unblocks", not "Blocked until": the near-term form is a countdown ("in 20h"),
// which only reads as English after a verb.
function blockWindow(entry: BlocklistEntry): string {
  if (!entry.blocked_until) return "Blocked permanently";
  if (!entry.active) return `Block expired ${timeAgo(entry.blocked_until)}`;
  return `Unblocks ${countdownOrDate(entry.blocked_until)}`;
}

export function HistoryRow({ event }: { event: GrabEvent }) {
  const { verb, icon: Icon, tone } = present(event);
  return (
    <Item className="gap-3">
      <ItemMedia>
        <span className={cn("grid size-8 place-items-center rounded-lg", tone)}>
          <Icon className="size-4" />
        </span>
      </ItemMedia>
      <ItemContent className="min-w-0 gap-0.5">
        <div className="text-sm font-medium">
          {verb} · Episode {event.item_number}
        </div>
        <div className="line-clamp-1 font-mono text-[12px] text-faint">
          {event.release_title}
        </div>
        {event.last_error && (
          <div className="line-clamp-2 text-[12px] text-destructive">
            {event.last_error}
          </div>
        )}
      </ItemContent>
      <ItemActions>
        <span className="text-xs text-faint">{timeAgo(event.created_at)}</span>
      </ItemActions>
    </Item>
  );
}
