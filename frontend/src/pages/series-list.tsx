import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { ChevronRight, Tv } from "lucide-react";
import { seriesQuery } from "@/lib/queries";
import { Topbar } from "@/components/topbar";
import { AddSeriesButton } from "@/components/add-series";
import { Poster } from "@/components/poster";
import { FormatBadge, MonitoredBadge } from "@/components/badges";
import { LibraryProgress } from "@/components/library-progress";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Skeleton } from "@/components/ui/skeleton";

export function SeriesListPage() {
  const { data: series, isLoading, isError, error } = useQuery(seriesQuery());

  return (
    <>
      <Topbar title="Titles" actions={<AddSeriesButton />} />

      <div className="px-4 py-6 sm:px-6">
        {isError && (
          <div className="rounded-lg border border-destructive/40 bg-destructive/5 px-4 py-3 text-sm text-destructive">
            Failed to load titles:{" "}
            {error instanceof Error ? error.message : String(error)}
          </div>
        )}

        {isLoading && <SeriesTableSkeleton />}

        {series && series.length === 0 && (
          <div className="mx-auto mt-10 flex max-w-md flex-col items-center rounded-lg border border-dashed bg-card px-6 py-16 text-center">
            <Tv className="mb-4 size-8 text-faint" />
            <h2 className="text-base font-semibold">No titles yet</h2>
            <p className="mb-5 mt-2 text-sm text-muted-foreground">
              Add a series or a film from AniList to start tracking and grabbing
              it.
            </p>
            <AddSeriesButton />
          </div>
        )}

        {series && series.length > 0 && (
          <div className="overflow-hidden rounded-lg border bg-card shadow-sm">
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Title</TableHead>
                    <TableHead className="hidden sm:table-cell">
                      Format
                    </TableHead>
                    <TableHead>Monitored</TableHead>
                    <TableHead className="sm:w-[200px]">Progress</TableHead>
                    <TableHead className="hidden w-8 sm:table-cell" />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {series.map((s) => (
                    <TableRow key={s.id} className="relative cursor-pointer">
                      <TableCell className="max-w-[42vw] sm:max-w-none">
                        <div className="flex items-center gap-3">
                          <Poster title={s.title} />
                          {/* Stretched link: keyboard-focusable and semantic, but its
                              hit area (after:inset-0) covers the whole row, and the
                              focus ring outlines the row. */}
                          <Link
                            to={`/series/${s.id}`}
                            className="truncate font-medium tracking-tight outline-none after:absolute after:inset-0 after:rounded-md focus-visible:after:ring-2 focus-visible:after:ring-inset focus-visible:after:ring-ring"
                          >
                            {s.title}
                          </Link>
                        </div>
                      </TableCell>
                      <TableCell className="hidden sm:table-cell">
                        <FormatBadge format={s.format} />
                      </TableCell>
                      <TableCell>
                        <MonitoredBadge monitored={s.monitored} />
                      </TableCell>
                      <TableCell>
                        <LibraryProgress
                          format={s.format}
                          inLibrary={s.in_library}
                          tracked={s.tracked}
                          monitored={s.monitored_items}
                          total={s.total}
                        />
                      </TableCell>
                      <TableCell className="hidden text-faint sm:table-cell">
                        <ChevronRight className="size-4" />
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          </div>
        )}
      </div>
    </>
  );
}

function SeriesTableSkeleton() {
  return (
    <div className="overflow-hidden rounded-lg border bg-card shadow-sm">
      {Array.from({ length: 4 }).map((_, i) => (
        <div
          key={i}
          className="flex items-center gap-3 border-b px-4 py-3 last:border-b-0"
        >
          <Skeleton className="h-12 w-[34px] rounded-[5px]" />
          <Skeleton className="h-4 w-48" />
          <Skeleton className="ml-auto h-5 w-16 rounded-full" />
          <Skeleton className="h-1.5 w-[140px]" />
        </div>
      ))}
    </div>
  );
}
