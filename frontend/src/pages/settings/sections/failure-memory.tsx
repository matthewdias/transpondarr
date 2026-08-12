import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Ban, Loader2, TriangleAlert } from "lucide-react";
import { toast } from "sonner";
import { api, type BlocklistSummary } from "@/lib/api";
import { blocklistSummaryQuery } from "@/lib/queries";
import { plural, timeAgo } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { SectionShell } from "../section-shell";

/**
 * The breaker's diagnosis, which is the point of surfacing it at all: an
 * operator waking to a wall of failed grabs should be told the client looks
 * unwell rather than left to infer it from a blocklist that stopped growing.
 */
function BreakerNotice({ breaker }: { breaker: BlocklistSummary["breaker"] }) {
  return (
    <div
      role="status"
      className="flex items-start gap-2.5 rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2.5"
    >
      <TriangleAlert className="mt-0.5 size-4 flex-none text-destructive" />
      <div className="min-w-0 text-[11px]">
        <p className="font-medium text-destructive">
          Not remembering failed releases right now
        </p>
        <p className="mt-0.5 text-muted-foreground">
          {plural(breaker.items, "different item")} failed on unrelated releases
          in the last {breaker.window_minutes} minutes
          {breaker.since && `, starting ${timeAgo(breaker.since)}`}. That many
          at once is the download client or the disk, not the releases, so
          blocking them would only take a healthy candidate pool out of
          circulation. Grabbing continues; this clears itself once failures
          stop.
        </p>
      </div>
    </div>
  );
}

export function FailureMemorySection() {
  const queryClient = useQueryClient();
  const { data, isLoading, isError, error } = useQuery(blocklistSummaryQuery());
  const [confirming, setConfirming] = useState(false);

  const clear = useMutation({
    mutationFn: () => api.clearBlocklist(),
    onSuccess: (cleared) => {
      setConfirming(false);
      toast.success(`${plural(cleared, "release")} unblocked`);
      queryClient.invalidateQueries({
        queryKey: blocklistSummaryQuery().queryKey,
      });
      queryClient.invalidateQueries({ queryKey: ["blocklist"] });
    },
    onError: (e) =>
      toast.error("Could not clear failure memory", {
        description: e instanceof Error ? e.message : String(e),
      }),
  });

  return (
    <SectionShell
      icon={Ban}
      title="Failure memory"
      description="Releases skipped because they already failed, and whether that memory is being recorded."
    >
      {isError && (
        <p className="text-xs text-destructive">
          Failed to load failure memory:{" "}
          {error instanceof Error ? error.message : String(error)}
        </p>
      )}
      {isLoading && <Skeleton className="h-5 w-56" />}
      {data && (
        <>
          {data.breaker.open && <BreakerNotice breaker={data.breaker} />}
          {data.blocked === 0 ? (
            <p className="text-xs text-muted-foreground">
              Nothing is blocked. Releases that fail are skipped for a day, then
              a week, then permanently on a third failure.
            </p>
          ) : (
            <div className="flex items-center justify-between gap-4">
              <p className="min-w-0 text-xs text-muted-foreground">
                <span className="font-medium text-foreground">
                  {plural(data.blocked, "release")}
                </span>{" "}
                skipped across {plural(data.titles, "series", "series")}.
              </p>
              {confirming ? (
                <span className="flex flex-none items-center gap-2">
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => setConfirming(false)}
                  >
                    Cancel
                  </Button>
                  <Button
                    variant="destructive"
                    size="sm"
                    disabled={clear.isPending}
                    onClick={() => clear.mutate()}
                  >
                    {clear.isPending && (
                      <Loader2 className="size-4 animate-spin" />
                    )}
                    Forget all
                  </Button>
                </span>
              ) : (
                <Button
                  variant="outline"
                  size="sm"
                  className="flex-none"
                  onClick={() => setConfirming(true)}
                >
                  Clear
                </Button>
              )}
            </div>
          )}
        </>
      )}
    </SectionShell>
  );
}
