import { Eye, EyeOff, Search } from "lucide-react";
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
  selected,
  onToggleSelect,
  onSetMonitored,
}: {
  detail: SeriesDetail;
  onSearchAll: () => void;
  onSearchItem: (n: number) => void;
  selected: Set<number>;
  onToggleSelect: (id: number) => void;
  onSetMonitored: (ids: number[], monitored: boolean) => void;
}) {
  const items = detail.items;
  // Progress is measured against what is actually being pursued, matching the
  // server's tracked count on the series list -- otherwise one bar reads 3 / 3
  // and the other 3 / 1050 for the same narrowed long-runner.
  const tracked = items.filter((i) => i.monitored);
  const total = tracked.length;
  const unmonitored = items.length - total;
  const count = (s: WantedItem["status"]) =>
    tracked.filter((i) => i.status === s).length;
  const inLibrary = count("in_library");
  const downloading = count("downloading");
  const stuck = count("stuck");
  const deferred = count("deferred");
  const wanted = total - inLibrary - downloading - stuck - deferred;
  const pct = (n: number) => (total > 0 ? (n / total) * 100 : 0);

  const selectedItems = items.filter((i) => selected.has(i.id));
  const selectedIds = selectedItems.map((i) => i.id);

  return (
    <div>
      <div className="mb-4 flex flex-col gap-3 rounded-lg border bg-card px-4 py-3 sm:flex-row sm:items-center sm:gap-4">
        <div className="flex min-w-0 flex-1 items-center gap-3.5">
          <div
            className="flex h-2.5 w-[120px] flex-none overflow-hidden rounded-md bg-foreground/10 ring-1 ring-inset ring-foreground/[0.07] sm:w-[200px]"
            title={`${inLibrary} in library · ${downloading} downloading · ${stuck} import blocked · ${deferred} batch downloaded · ${wanted} wanted`}
          >
            {inLibrary > 0 && (
              <span
                className="h-full min-w-1.5 flex-none bg-have"
                style={{ width: `${pct(inLibrary)}%` }}
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
              {inLibrary} / {total}
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
            {unmonitored > 0 && (
              <>
                <span className="mx-1 text-faint">·</span>
                <span className="text-faint">{unmonitored} not monitored</span>
              </>
            )}
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

      {selectedIds.length > 0 && (
        <div className="mb-3 flex flex-wrap items-center gap-2 rounded-lg border bg-panel-2 px-3.5 py-2.5">
          <span className="text-[13px] font-medium">
            {selectedIds.length} selected
          </span>
          <div className="ml-auto flex gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => onSetMonitored(selectedIds, true)}
            >
              <Eye className="size-4" /> Monitor {selectedIds.length}
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => onSetMonitored(selectedIds, false)}
            >
              <EyeOff className="size-4" /> Unmonitor {selectedIds.length}
            </Button>
          </div>
        </div>
      )}

      <div className="overflow-hidden rounded-lg border bg-card shadow-sm">
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-8" />
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
                <EpisodeRow
                  key={item.id}
                  item={item}
                  selected={selected.has(item.id)}
                  onToggleSelect={onToggleSelect}
                  onSetMonitored={onSetMonitored}
                  onSearch={onSearchItem}
                />
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
  selected,
  onToggleSelect,
  onSetMonitored,
  onSearch,
}: {
  item: WantedItem;
  selected: boolean;
  onToggleSelect: (id: number) => void;
  onSetMonitored: (ids: number[], monitored: boolean) => void;
  onSearch: (n: number) => void;
}) {
  return (
    <TableRow
      className={cn(
        item.status === "wanted" && "text-muted-foreground",
        !item.monitored && "opacity-60",
      )}
    >
      <TableCell>
        {/* A raw checkbox, as on the Wanted page: there is no shadcn Checkbox
            in this project and this is not the change that adds one. */}
        <input
          type="checkbox"
          checked={selected}
          onChange={() => onToggleSelect(item.id)}
          className="size-3.5 accent-primary"
          aria-label={`Select episode ${item.number}`}
        />
      </TableCell>
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
        <div className="flex items-center justify-end gap-2">
          {/* Search stays offered on an unmonitored episode: monitoring never
              gates a manual path (PR #57, generalised by #188). */}
          {(item.status === "wanted" || item.status === "deferred") && (
            <button
              className="text-sm font-medium text-accent-foreground hover:underline"
              onClick={() => onSearch(item.number)}
            >
              Search
            </button>
          )}
          <button
            className="text-faint hover:text-foreground"
            title={item.monitored ? "Stop monitoring" : "Monitor"}
            aria-label={
              item.monitored
                ? `Stop monitoring episode ${item.number}`
                : `Monitor episode ${item.number}`
            }
            onClick={() => onSetMonitored([item.id], !item.monitored)}
          >
            {item.monitored ? (
              <Eye className="size-4" />
            ) : (
              <EyeOff className="size-4" />
            )}
          </button>
        </div>
      </TableCell>
    </TableRow>
  );
}
