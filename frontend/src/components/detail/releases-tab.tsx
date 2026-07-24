import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Loader2, RefreshCw, Search, TriangleAlert } from "lucide-react";
import { api, ApiError, type CandidateRelease } from "@/lib/api";
import {
  grabsQuery,
  releasesQuery,
  seriesDetailQuery,
  seriesQuery,
} from "@/lib/queries";
import { formatBytes } from "@/lib/format";
import { cn } from "@/lib/utils";
import { useIsMobile } from "@/hooks/use-mobile";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Drawer,
  DrawerContent,
  DrawerHeader,
  DrawerTitle,
} from "@/components/ui/drawer";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";

function matchLabel(items?: number[] | null): string {
  if (!items || items.length === 0) return "";
  if (items.length === 1) return `E${items[0]}`;
  const sorted = [...items].sort((a, b) => a - b);
  const contiguous = sorted.every((n, i) => i === 0 || n === sorted[i - 1] + 1);
  if (contiguous) return `E${sorted[0]}–${sorted[sorted.length - 1]} · batch`;
  return `${items.length} eps`;
}

function quality(r: CandidateRelease): string {
  return (
    [r.resolution || null, r.dual_audio ? "Dual" : null]
      .filter(Boolean)
      .join(" · ") || "—"
  );
}

function signed(points: number): string {
  return points > 0 ? `+${points}` : String(points);
}

// ScoreBreakdown is the "why this rank" surface (#17): per-axis contributions,
// the total, and — when the profile refuses the release — the reason.
export function ScoreBreakdown({ r }: { r: CandidateRelease }) {
  const parts = r.score_parts ?? [];
  return (
    <div className="min-w-44">
      {r.ineligible_reason && (
        <p className="mb-2 max-w-56 text-xs font-medium text-dl">
          {r.ineligible_reason}
        </p>
      )}
      {parts.length === 0 ? (
        <p className="text-xs text-faint">
          No profile preferences matched this release.
        </p>
      ) : (
        <dl>
          {parts.map((p) => (
            <div
              key={p.label}
              className="flex items-baseline justify-between gap-4 py-0.5 text-xs"
            >
              <dt className="text-muted-foreground">{p.label}</dt>
              <dd className="font-medium tabular-nums">{signed(p.points)}</dd>
            </div>
          ))}
          <div className="mt-1 flex items-baseline justify-between gap-4 border-t pt-1.5 text-xs font-semibold">
            <dt>Total</dt>
            <dd className="tabular-nums">{r.score}</dd>
          </div>
        </dl>
      )}
    </div>
  );
}

export function ScoreCell({ r }: { r: CandidateRelease }) {
  return (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger asChild>
          <span
            className={cn(
              "inline-flex cursor-default items-center gap-1 font-semibold tabular-nums",
              !r.eligible && "text-dl",
            )}
          >
            {!r.eligible && (
              <TriangleAlert aria-label="ineligible" className="size-3.5" />
            )}
            {r.score}
          </span>
        </TooltipTrigger>
        <TooltipContent side="left">
          <ScoreBreakdown r={r} />
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}

function MatchCell({ r }: { r: CandidateRelease }) {
  if (r.matched) {
    return (
      <span className="inline-flex items-center rounded-full border border-transparent bg-have-weak px-2.5 py-0.5 text-[11.5px] font-semibold text-have">
        {matchLabel(r.items)}
      </span>
    );
  }
  return <span className="text-xs italic text-faint">{r.reason}</span>;
}

export function ReleasesTab({
  seriesId,
  active,
}: {
  seriesId: number;
  active: boolean;
}) {
  const isMobile = useIsMobile();
  const queryClient = useQueryClient();
  const [selected, setSelected] = useState<CandidateRelease | null>(null);
  // download_urls already sent this session, so a grabbed row can't be re-sent to
  // the download client on a second click (the search result is computed once and
  // doesn't refresh after a grab).
  const [grabbed, setGrabbed] = useState<Set<string>>(new Set());

  const search = useQuery({
    ...releasesQuery(seriesId),
    enabled: active,
  });

  const grab = useMutation({
    mutationFn: (r: CandidateRelease) =>
      api.grabRelease(seriesId, r.download_url),
    onSuccess: (res, r) => {
      setGrabbed((prev) => new Set(prev).add(r.download_url));
      if (res.ineligible_reason) {
        toast.warning("Grabbed despite the profile", {
          description: res.ineligible_reason,
        });
      } else {
        toast.success("Grab sent to download client", {
          description: `${res.release} · ${res.outcome}`,
        });
      }
      queryClient.invalidateQueries({
        queryKey: seriesDetailQuery(seriesId).queryKey,
      });
      queryClient.invalidateQueries({
        queryKey: grabsQuery(seriesId).queryKey,
      });
      queryClient.invalidateQueries({ queryKey: seriesQuery().queryKey });
      setSelected(null);
    },
    onError: (err) => {
      toast.error("Grab failed", {
        description: err instanceof Error ? err.message : String(err),
      });
    },
  });

  const grabbing = (r: CandidateRelease) =>
    grab.isPending && grab.variables?.download_url === r.download_url;

  if (search.isError) {
    const msg =
      search.error instanceof ApiError
        ? search.error.message
        : String(search.error);
    return (
      <div className="flex flex-col items-center rounded-lg border border-dashed bg-card px-6 py-14 text-center">
        <TriangleAlert className="mb-3 size-7 text-dl" />
        <h3 className="text-sm font-semibold">Couldn’t search for releases</h3>
        <p className="mt-1.5 max-w-md text-sm text-muted-foreground">{msg}</p>
        <Button
          variant="outline"
          size="sm"
          className="mt-4"
          onClick={() => search.refetch()}
        >
          <RefreshCw className="size-4" /> Try again
        </Button>
      </div>
    );
  }

  const results = search.data?.results ?? [];

  return (
    <div>
      <div className="mb-3 flex items-center gap-3">
        <h2 className="text-[13px] font-semibold uppercase tracking-wide text-faint">
          Search results
        </h2>
        <span className="h-px flex-1 bg-border" />
        <span className="hidden text-xs text-faint sm:inline">
          matched against wanted items — season &amp; number aware
        </span>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => search.refetch()}
          disabled={search.isFetching}
        >
          <RefreshCw
            className={cn("size-4", search.isFetching && "animate-spin")}
          />
          Search
        </Button>
      </div>

      {search.isLoading && (
        <div className="flex items-center justify-center gap-2 rounded-lg border bg-card py-16 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" /> Searching indexers…
        </div>
      )}

      {!search.isLoading && results.length === 0 && (
        <div className="flex flex-col items-center rounded-lg border border-dashed bg-card py-16 text-center">
          <Search className="mb-3 size-7 text-faint" />
          <p className="text-sm text-muted-foreground">
            No releases found for this series.
          </p>
        </div>
      )}

      {results.length > 0 && (
        <div className="overflow-hidden rounded-lg border bg-card shadow-sm">
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Release</TableHead>
                  <TableHead className="hidden md:table-cell">Group</TableHead>
                  <TableHead className="hidden sm:table-cell">
                    Quality
                  </TableHead>
                  <TableHead className="text-right">Size</TableHead>
                  <TableHead className="hidden text-right sm:table-cell">
                    Seed
                  </TableHead>
                  <TableHead className="hidden text-right sm:table-cell">
                    Score
                  </TableHead>
                  <TableHead>Match</TableHead>
                  <TableHead className="w-16" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {results.map((r, i) => (
                  <TableRow
                    key={r.download_url || i}
                    className={cn(
                      !r.matched && "opacity-60",
                      isMobile && "cursor-pointer",
                    )}
                    onClick={isMobile ? () => setSelected(r) : undefined}
                  >
                    <TableCell className="max-w-[280px]">
                      <span className="line-clamp-2 font-mono text-[12.5px] tracking-tight">
                        {r.title}
                      </span>
                    </TableCell>
                    <TableCell className="hidden text-muted-foreground md:table-cell">
                      {r.release_group || "—"}
                    </TableCell>
                    <TableCell className="hidden text-muted-foreground sm:table-cell">
                      {quality(r)}
                    </TableCell>
                    <TableCell className="text-right tabular-nums">
                      {formatBytes(r.size)}
                    </TableCell>
                    <TableCell className="hidden text-right tabular-nums sm:table-cell">
                      {r.seeders}
                    </TableCell>
                    <TableCell className="hidden text-right sm:table-cell">
                      <ScoreCell r={r} />
                    </TableCell>
                    <TableCell>
                      <MatchCell r={r} />
                    </TableCell>
                    <TableCell className="text-right">
                      {r.matched && (
                        <Button
                          variant="outline"
                          size="sm"
                          className="hidden sm:inline-flex"
                          disabled={
                            grab.isPending || grabbed.has(r.download_url)
                          }
                          onClick={(e) => {
                            e.stopPropagation();
                            grab.mutate(r);
                          }}
                        >
                          {grabbing(r) && (
                            <Loader2 className="size-3.5 animate-spin" />
                          )}
                          {grabbed.has(r.download_url) ? "Grabbed" : "Grab"}
                        </Button>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </div>
      )}

      <Drawer open={!!selected} onOpenChange={(o) => !o && setSelected(null)}>
        <DrawerContent className="px-4 pb-6">
          {selected && (
            <>
              <DrawerHeader className="px-0">
                <DrawerTitle className="text-xs font-semibold uppercase tracking-wide text-faint">
                  Release
                </DrawerTitle>
              </DrawerHeader>
              <p className="mb-4 break-all font-mono text-[13.5px]">
                {selected.title}
              </p>
              <dl className="mb-5 grid grid-cols-2 gap-x-4 gap-y-3.5">
                <Fact k="Group" v={selected.release_group || "—"} />
                <Fact k="Quality" v={quality(selected)} />
                <Fact k="Size" v={formatBytes(selected.size)} />
                <Fact k="Seeders" v={String(selected.seeders)} />
                <div className="col-span-2">
                  <dt className="text-[11px] uppercase tracking-wide text-faint">
                    Match
                  </dt>
                  <dd className="mt-1.5">
                    <MatchCell r={selected} />
                  </dd>
                </div>
                <div className="col-span-2">
                  <dt className="mb-1.5 text-[11px] uppercase tracking-wide text-faint">
                    Score
                  </dt>
                  <dd className="rounded-md border bg-muted/40 p-3">
                    <ScoreBreakdown r={selected} />
                  </dd>
                </div>
              </dl>
              <div className="flex gap-2.5">
                <Button
                  variant="outline"
                  className="flex-1"
                  onClick={() => setSelected(null)}
                >
                  Close
                </Button>
                <Button
                  className="flex-1"
                  disabled={
                    !selected.matched ||
                    grab.isPending ||
                    grabbed.has(selected.download_url)
                  }
                  onClick={() => grab.mutate(selected)}
                >
                  {grabbing(selected) && (
                    <Loader2 className="size-4 animate-spin" />
                  )}
                  {grabbed.has(selected.download_url) ? "Grabbed" : "Grab"}
                </Button>
              </div>
            </>
          )}
        </DrawerContent>
      </Drawer>
    </div>
  );
}

function Fact({ k, v }: { k: string; v: string }) {
  return (
    <div>
      <dt className="text-[11px] uppercase tracking-wide text-faint">{k}</dt>
      <dd className="mt-0.5 font-semibold tabular-nums">{v}</dd>
    </div>
  );
}
