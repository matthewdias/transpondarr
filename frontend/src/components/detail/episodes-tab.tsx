import { memo, useCallback, useMemo, useRef } from "react";
import { Eye, EyeOff, Search } from "lucide-react";
import type { SeriesDetail, WantedItem } from "@/lib/api";
import { airDate, pad2, parseTimestamp } from "@/lib/format";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { ItemStatusBadge, UnmonitoredItemBadge } from "@/components/badges";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";

function Sep() {
  return <span className="mx-1 text-faint">·</span>;
}

// indeterminate is a DOM property with no HTML attribute, so it can only be set
// through the node -- a partial selection has to read as partial rather than as
// "none selected", which is what the box would otherwise claim.
function SelectAllCheckbox({
  total,
  selectedCount,
  onChange,
}: {
  total: number;
  selectedCount: number;
  onChange: (all: boolean) => void;
}) {
  const all = total > 0 && selectedCount === total;
  const some = selectedCount > 0 && !all;
  return (
    <input
      type="checkbox"
      ref={(el) => {
        if (el) el.indeterminate = some;
      }}
      checked={all}
      disabled={total === 0}
      onChange={() => onChange(!all)}
      className="size-3.5 accent-primary"
      aria-label={all ? "Deselect all episodes" : "Select all episodes"}
    />
  );
}

export function EpisodesTab({
  detail,
  onSearchAll,
  onSearchItem,
  selected,
  onToggleSelect,
  onSelectRange,
  onSetSelection,
  onSetMonitored,
}: {
  detail: SeriesDetail;
  onSearchAll: () => void;
  onSearchItem: (n: number) => void;
  selected: Set<number>;
  onToggleSelect: (id: number) => void;
  onSelectRange: (ids: number[]) => void;
  onSetSelection: (ids: number[]) => void;
  onSetMonitored: (ids: number[], monitored: boolean) => void;
}) {
  const items = detail.items;

  // Memoized because a 1,200-row series recomputes these on every selection
  // change otherwise, and none of them depend on the selection.
  const counts = useMemo(() => {
    // Exactly ListSeriesWithProgress's definition of tracked -- monitored AND
    // already broadcast -- so this strip and the series-list bar can never
    // disagree about the same series. A null air date reads as aired, because
    // AniList's schedule coverage is thin by design.
    const now = Date.now();
    const aired = (i: WantedItem) =>
      !i.airs_at || parseTimestamp(i.airs_at) <= now;
    const tracked = items.filter((i) => i.monitored && aired(i));
    const of = (s: WantedItem["status"]) =>
      tracked.filter((i) => i.status === s).length;
    const inLibrary = of("in_library");
    const downloading = of("downloading");
    const stuck = of("stuck");
    const deferred = of("deferred");
    // The three categories partition every item, so a reader can add them up to
    // the total -- which is why unaired has to be named rather than dropped.
    const unmonitored = items.filter((i) => !i.monitored).length;
    return {
      total: tracked.length,
      unaired: items.length - tracked.length - unmonitored,
      unmonitored,
      inLibrary,
      downloading,
      stuck,
      deferred,
      wanted: tracked.length - inLibrary - downloading - stuck - deferred,
    };
  }, [items]);
  const {
    total,
    unaired,
    unmonitored,
    inLibrary,
    downloading,
    stuck,
    deferred,
    wanted,
  } = counts;
  const pct = (n: number) => (total > 0 ? (n / total) * 100 : 0);
  // "0 / 0" reads as "this series has no episodes", which is exactly wrong for
  // a show you are waiting on.
  const nothingAired = total === 0 && items.length > 0;

  // The shift-click anchor is the last row clicked, not part of the selection,
  // and moving it must not re-render 1,200 rows -- hence a ref.
  const anchor = useRef<number | null>(null);
  const onSelect = useCallback(
    (id: number, index: number, shift: boolean) => {
      const from = anchor.current;
      if (shift && from !== null && from !== index) {
        const [lo, hi] = from < index ? [from, index] : [index, from];
        onSelectRange(items.slice(lo, hi + 1).map((i) => i.id));
      } else {
        onToggleSelect(id);
      }
      anchor.current = index;
    },
    [items, onSelectRange, onToggleSelect],
  );

  return (
    <div>
      <div className="mb-4 flex flex-col gap-3 rounded-lg border bg-card px-4 py-3 sm:flex-row sm:items-center sm:gap-4">
        <div className="flex min-w-0 flex-1 items-center gap-3.5">
          <div
            className="flex h-2.5 w-[120px] flex-none overflow-hidden rounded-md bg-foreground/10 ring-1 ring-inset ring-foreground/[0.07] sm:w-[200px]"
            title={`${inLibrary} in library · ${downloading} downloading · ${stuck} import blocked · ${deferred} batch downloaded · ${wanted} wanted · ${unaired} not yet aired · ${unmonitored} not monitored`}
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
            {nothingAired ? (
              <b className="font-semibold text-foreground">Nothing aired yet</b>
            ) : (
              <>
                <b className="font-semibold tabular-nums text-foreground">
                  {inLibrary} / {total}
                </b>{" "}
                in library
                {downloading > 0 && (
                  <>
                    <Sep />
                    <span className="font-semibold text-dl">
                      {downloading} downloading
                    </span>
                  </>
                )}
                {stuck > 0 && (
                  <>
                    <Sep />
                    <span className="font-semibold text-destructive">
                      {stuck} import blocked
                    </span>
                  </>
                )}
                {deferred > 0 && (
                  <>
                    <Sep />
                    <span className="font-semibold text-dl">
                      {deferred} batch downloaded
                    </span>
                  </>
                )}
                <Sep />
                {wanted} wanted
              </>
            )}
            {unaired > 0 && (
              <>
                <Sep />
                <span className="text-faint">{unaired} not yet aired</span>
              </>
            )}
            {unmonitored > 0 && (
              <>
                <Sep />
                <span className="text-faint">{unmonitored} not monitored</span>
              </>
            )}
            {/* The denominator is a subset, so the raw count has to stay on the
                strip that carries the ratio -- as the series-list bar does. */}
            {total !== items.length && (
              <>
                <Sep />
                <span className="text-faint">{items.length} total</span>
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

      <div className="overflow-hidden rounded-lg border bg-card shadow-sm">
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-8">
                  <SelectAllCheckbox
                    total={items.length}
                    selectedCount={selected.size}
                    onChange={(all) =>
                      onSetSelection(all ? items.map((i) => i.id) : [])
                    }
                  />
                </TableHead>
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
              {items.map((item, index) => (
                <EpisodeRow
                  key={item.id}
                  item={item}
                  index={index}
                  selected={selected.has(item.id)}
                  onSelect={onSelect}
                  onSetMonitored={onSetMonitored}
                  onSearch={onSearchItem}
                />
              ))}
            </TableBody>
          </Table>
        </div>
      </div>

      {/* Anchored to the viewport rather than inserted above the table: in flow
          it shoved a thousand rows down under the cursor on the first click, and
          scrolled out of reach a few hundred rows in. */}
      {selected.size > 0 && (
        <div className="sticky bottom-4 z-20 mt-3 flex flex-wrap items-center gap-2 rounded-lg border bg-card px-3.5 py-2.5 shadow-lg">
          <span className="text-[13px] font-medium">
            {selected.size} selected
          </span>
          <div className="ml-auto flex gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => onSetMonitored([...selected], true)}
            >
              <Eye className="size-4" /> Monitor {selected.size}
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => onSetMonitored([...selected], false)}
            >
              <EyeOff className="size-4" /> Unmonitor {selected.size}
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}

// Memoized, and every prop above is referentially stable, so toggling one
// checkbox re-renders one row instead of the whole series.
const EpisodeRow = memo(function EpisodeRow({
  item,
  index,
  selected,
  onSelect,
  onSetMonitored,
  onSearch,
}: {
  item: WantedItem;
  index: number;
  selected: boolean;
  onSelect: (id: number, index: number, shift: boolean) => void;
  onSetMonitored: (ids: number[], monitored: boolean) => void;
  onSearch: (n: number) => void;
}) {
  const unmonitoredWanted = !item.monitored && item.status === "wanted";
  return (
    <TableRow
      className={cn(item.status === "wanted" && "text-muted-foreground")}
    >
      <TableCell>
        {/* A raw checkbox, as on the Wanted page: there is no shadcn Checkbox
            in this project and this is not the change that adds one. The click
            handler is what carries shiftKey; onChange is the no-op React wants
            beside a controlled checked. */}
        <input
          type="checkbox"
          checked={selected}
          onChange={() => {}}
          onClick={(e) => onSelect(item.id, index, e.shiftKey)}
          className="size-3.5 accent-primary"
          aria-label={`Select episode ${item.number}`}
        />
      </TableCell>
      <TableCell className="font-mono font-semibold tabular-nums">
        {pad2(item.number)}
      </TableCell>
      <TableCell>
        {unmonitoredWanted ? (
          <UnmonitoredItemBadge />
        ) : (
          <ItemStatusBadge status={item.status} error={item.import_error} />
        )}
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
});
