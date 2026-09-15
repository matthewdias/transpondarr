import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Ban, ChevronDown, ChevronRight, History } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { api, type BlocklistEntry, errorReason } from "@/lib/api";
import { GrabEventRow } from "@/components/grab-event-row";
import { blocklistQuery, grabsQuery } from "@/lib/queries";
import { countdownOrDate, plural, timeAgo } from "@/lib/format";
import { cn } from "@/lib/utils";
import { EmptyState } from "@/components/empty-state";
import { LoadError } from "@/components/load-error";
import { Button } from "@/components/ui/button";
import {
  Item,
  ItemContent,
  ItemMedia,
  ItemActions,
  ItemGroup,
} from "@/components/ui/item";
import { Skeleton } from "@/components/ui/skeleton";

export function HistoryTab({
  titleId,
  active,
}: {
  titleId: number;
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
    ...grabsQuery(titleId),
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
    return <LoadError what="history" error={error} onRetry={() => refetch()} />;
  }

  return (
    <div className="space-y-6">
      {!events || events.length === 0 ? (
        <EmptyState
          icon={History}
          blurb="No grab or import history yet. Grab a release from the Releases tab."
          width="section"
        />
      ) : (
        <ItemGroup className="overflow-hidden rounded-lg border bg-card shadow-sm [&>*+*]:border-t">
          {events.map((e) => (
            <GrabEventRow key={e.id} event={e} />
          ))}
        </ItemGroup>
      )}
      <BlockedReleases titleId={titleId} active={active} />
    </div>
  );
}

// Blocklist entries outlast grab rows, so this is its own section and the feed's
// empty state must not hide it.
export function BlockedReleases({
  titleId,
  active,
}: {
  titleId: number;
  active: boolean;
}) {
  const {
    data: entries,
    isError,
    error,
    refetch,
  } = useQuery({
    ...blocklistQuery(titleId),
    enabled: active,
  });
  // null until the user decides, so the default can depend on data the first
  // render does not have yet.
  const [showExpired, setShowExpired] = useState<boolean | null>(null);
  const clear = useClearBlocklist(titleId);

  if (isError) {
    return (
      <section>
        <h3 className="mb-2 text-sm font-semibold">Blocked releases</h3>
        <LoadError
          what="blocked releases"
          error={error}
          onRetry={() => refetch()}
        />
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
      <div className="mb-2 flex items-center justify-between gap-4">
        <h3 className="text-sm font-semibold">Blocked releases</h3>
        {blocking.length > 0 && (
          <Button
            variant="outline"
            size="sm"
            disabled={clear.isPending}
            onClick={() => clear.mutate(false)}
          >
            Unblock all
          </Button>
        )}
      </div>
      {blocking.length > 0 && (
        <>
          <p className="mb-3 text-sm text-muted-foreground">
            Releases that failed and are skipped when ranking. Each repeat
            failure blocks for longer; the third blocks permanently.
          </p>
          <BlockedList titleId={titleId} entries={blocking} />
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
            {plural(expired.length, "expired block")}
          </Button>
          {expandExpired && (
            <div className="mt-2">
              <p className="mb-3 text-sm text-muted-foreground">
                No longer skipped when ranking. Kept as history — a re-grab
                overwrites the failed grab row, and the failure count still
                escalates if the release fails again.
              </p>
              <BlockedList titleId={titleId} entries={expired} />
              <Button
                variant="ghost"
                size="sm"
                className="mt-2 text-muted-foreground"
                disabled={clear.isPending}
                onClick={() => clear.mutate(true)}
              >
                Forget expired
              </Button>
            </div>
          )}
        </>
      )}
    </section>
  );
}

// Bulk unblock. An environmental fault blocks a whole candidate pool at once,
// and clearing that one blocklist entry at a time is the problem, not the recovery.
function useClearBlocklist(titleId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (expiredOnly: boolean) =>
      api.clearTitleBlocklist(titleId, expiredOnly),
    onSuccess: (cleared) => {
      toast.success(`${plural(cleared, "release")} unblocked`);
      queryClient.invalidateQueries({
        queryKey: blocklistQuery(titleId).queryKey,
      });
      queryClient.invalidateQueries({
        queryKey: grabsQuery(titleId).queryKey,
      });
    },
    onError: (e) =>
      toast.error("Couldn’t unblock the releases", {
        description: errorReason(e),
      }),
  });
}

function BlockedList({
  titleId,
  entries,
}: {
  titleId: number;
  entries: BlocklistEntry[];
}) {
  return (
    <ItemGroup className="overflow-hidden rounded-lg border bg-card shadow-sm [&>*+*]:border-t">
      {entries.map((e) => (
        <BlockedRow key={e.id} titleId={titleId} entry={e} />
      ))}
    </ItemGroup>
  );
}

function BlockedRow({
  titleId,
  entry,
}: {
  titleId: number;
  entry: BlocklistEntry;
}) {
  const queryClient = useQueryClient();
  const unblock = useMutation({
    mutationFn: () => api.clearBlocklistEntry(titleId, entry.id),
    onSuccess: () => {
      toast.success("Release unblocked");
      queryClient.invalidateQueries({
        queryKey: blocklistQuery(titleId).queryKey,
      });
      queryClient.invalidateQueries({
        queryKey: grabsQuery(titleId).queryKey,
      });
    },
    onError: (e) =>
      toast.error("Couldn’t unblock the release", {
        description: errorReason(e),
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
        <div className="line-clamp-1 font-mono text-xs">
          {entry.release_title}
        </div>
        <div className="text-xs text-muted-foreground">
          {entry.reason}
          {entry.failures > 1 && ` · ${entry.failures} failures`}
        </div>
        <div className="text-xs text-faint">{blockWindow(entry)}</div>
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
