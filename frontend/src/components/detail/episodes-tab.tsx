import { Search } from "lucide-react";
import type { SeriesDetail, WantedItem } from "@/lib/api";
import { airDate, pad2 } from "@/lib/format";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { ItemStatusBadge } from "@/components/badges";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";

export function EpisodesTab({
  detail,
  onSearchAll,
  onSearchItem,
}: {
  detail: SeriesDetail;
  onSearchAll: () => void;
  onSearchItem: (n: number) => void;
}) {
  const items = detail.items;
  const total = items.length;
  const have = items.filter((i) => i.status === "have").length;
  const downloading = items.filter((i) => i.status === "downloading").length;
  const stuck = items.filter((i) => i.status === "stuck").length;
  const deferred = items.filter((i) => i.status === "deferred").length;
  const wanted = total - have - downloading - stuck - deferred;
  const pct = (n: number) => (total > 0 ? (n / total) * 100 : 0);

  return (
    <div>
      <div className="mb-4 flex flex-col gap-3 rounded-lg border bg-card px-4 py-3 sm:flex-row sm:items-center sm:gap-4">
        <div className="flex min-w-0 flex-1 items-center gap-3.5">
          <div
            className="flex h-2.5 w-[120px] flex-none overflow-hidden rounded-md bg-foreground/10 ring-1 ring-inset ring-foreground/[0.07] sm:w-[200px]"
            title={`${have} in library · ${downloading} downloading · ${stuck} import blocked · ${deferred} batch downloaded · ${wanted} wanted`}
          >
            {have > 0 && (
              <span
                className="h-full min-w-1.5 flex-none bg-have"
                style={{ width: `${pct(have)}%` }}
              />
            )}
            {downloading > 0 && (
              <span
                className="h-full min-w-1.5 flex-none bg-dl"
                style={{ width: `${pct(downloading)}%` }}
              />
            )}
          </div>
          <div className="min-w-0 text-[13px] text-muted-foreground">
            <b className="font-semibold tabular-nums text-foreground">
              {have} / {total}
            </b>{" "}
            in library
            {downloading > 0 && (
              <>
                <span className="mx-1 text-faint">·</span>
                <span className="font-semibold text-dl">
                  {downloading} downloading
                </span>
              </>
            )}
            {stuck > 0 && (
              <>
                <span className="mx-1 text-faint">·</span>
                <span className="font-semibold text-destructive">
                  {stuck} import blocked
                </span>
              </>
            )}
            {deferred > 0 && (
              <>
                <span className="mx-1 text-faint">·</span>
                <span className="font-semibold text-dl">
                  {deferred} batch downloaded
                </span>
              </>
            )}
            <span className="mx-1 text-faint">·</span>
            {wanted} wanted
          </div>
        </div>
        <Button
          variant="outline"
          size="sm"
          className="w-full sm:ml-auto sm:w-auto"
          onClick={onSearchAll}
          disabled={wanted + deferred === 0}
        >
          <Search className="size-4" /> Search all wanted
        </Button>
      </div>

      <div className="overflow-hidden rounded-lg border bg-card shadow-sm">
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-[70px]">Ep</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="hidden w-32 md:table-cell">
                  Airs
                </TableHead>
                <TableHead className="hidden sm:table-cell">Quality</TableHead>
                <TableHead className="w-24 text-right" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((item) => (
                <EpisodeRow key={item.id} item={item} onSearch={onSearchItem} />
              ))}
            </TableBody>
          </Table>
        </div>
      </div>
    </div>
  );
}

function EpisodeRow({
  item,
  onSearch,
}: {
  item: WantedItem;
  onSearch: (n: number) => void;
}) {
  return (
    <TableRow
      className={cn(item.status === "wanted" && "text-muted-foreground")}
    >
      <TableCell className="font-mono font-semibold tabular-nums">
        {pad2(item.number)}
      </TableCell>
      <TableCell>
        <ItemStatusBadge status={item.status} error={item.import_error} />
      </TableCell>
      <TableCell
        className="hidden whitespace-nowrap text-muted-foreground md:table-cell"
        title={item.airs_at}
      >
        {item.airs_at ? (
          airDate(item.airs_at)
        ) : (
          <span className="text-faint">—</span>
        )}
      </TableCell>
      <TableCell className="hidden text-muted-foreground sm:table-cell">
        {item.release_title ? (
          <span className="block max-w-[420px] truncate font-mono text-xs">
            {item.release_title}
          </span>
        ) : (
          <span className="text-faint">—</span>
        )}
      </TableCell>
      <TableCell className="text-right">
        {(item.status === "wanted" || item.status === "deferred") && (
          <button
            className="text-sm font-medium text-accent-foreground hover:underline"
            onClick={() => onSearch(item.number)}
          >
            Search
          </button>
        )}
      </TableCell>
    </TableRow>
  );
}
