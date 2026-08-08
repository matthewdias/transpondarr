import { memo, useCallback, useMemo, useRef } from "react";
import { Eye, EyeOff, Search } from "lucide-react";
import type { SeriesDetail, WantedItem } from "@/lib/api";
import { airDate, pad2, parseTimestamp } from "@/lib/format";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { ItemStatusBadge, UnmonitoredItemBadge } from "@/components/badges";
import { MonitorToggle } from "@/components/monitor-toggle";
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
  return (
    <Checkbox
      checked={all ? true : selectedCount > 0 ? "indeterminate" : false}
      disabled={total === 0}
      onCheckedChange={() => onChange(!all)}
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
    // Exactly ListSeriesWithProgress's definition of tracked, so this strip and
    // the series-list bar can never disagree about the same series.
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
    // The three categories partition every item, so a reader can add them up.
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
  // A zero denominator has two causes, and naming the wrong one is a plain false
  // statement on a series whose episodes have all aired and all been switched off.
  const empty = total === 0 && items.length > 0;
  const emptyLabel =
    unmonitored === items.length ? "Nothing monitored" : "Nothing aired yet";

  // An id, not an index: a refetch between two clicks can renumber the rows, and
  // a stale id resolves to -1 and degrades to a plain toggle rather than to the
  // wrong range. Not part of the selection, so moving it re-renders nothing.
  const anchor = useRef<number | null>(null);
  const onSelect = useCallback(
    (id: number, shift: boolean) => {
      const from = anchor.current;
      const fromIndex =
        from === null ? -1 : items.findIndex((i) => i.id === from);
      const index = items.findIndex((i) => i.id === id);
      if (shift && fromIndex >= 0 && index >= 0 && fromIndex !== index) {
        const [lo, hi] =
          fromIndex < index ? [fromIndex, index] : [index, fromIndex];
        onSelectRange(items.slice(lo, hi + 1).map((i) => i.id));
      } else {
        onToggleSelect(id);
      }
      anchor.current = id;
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
            {empty ? (
              <b className="font-semibold text-foreground">{emptyLabel}</b>
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
              {items.map((item) => (
                <EpisodeRow
                  key={item.id}
                  item={item}
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

      {/* Anchored to the viewport: in flow it shoved a thousand rows down under
          the cursor and scrolled out of reach a few hundred rows in. */}
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
  selected,
  onSelect,
  onSetMonitored,
  onSearch,
}: {
  item: WantedItem;
  selected: boolean;
  onSelect: (id: number, shift: boolean) => void;
  onSetMonitored: (ids: number[], monitored: boolean) => void;
  onSearch: (n: number) => void;
}) {
  const unmonitoredWanted = !item.monitored && item.status === "wanted";
  return (
    <TableRow
      className={cn(item.status === "wanted" && "text-muted-foreground")}
    >
      <TableCell>
        {/* onClick, not onCheckedChange: it is what carries shiftKey. */}
        <Checkbox
          checked={selected}
          onClick={(e) => onSelect(item.id, e.shiftKey)}
          aria-label={`Select episode ${item.number}`}
        />
      </TableCell>
      <TableCell className="font-mono font-semibold tabular-nums">
        {pad2(item.number)}
      </TableCell>
      <TableCell>
        {/* Substituted, not qualified: every other status stays true when
            unmonitored, and the row's toggle already shows the state. */}
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
          {/* Nothing gates a manual path (PR #57, generalised by #188), so a held
              item is offered as an upgrade; the two withheld statuses are exactly
              the unsettled grab, whose row a second grab would overwrite. */}
          {item.status !== "downloading" && item.status !== "stuck" && (
            <button
              className="text-sm font-medium text-accent-foreground hover:underline"
              onClick={() => onSearch(item.number)}
            >
              Search
            </button>
          )}
          <MonitorToggle
            monitored={item.monitored}
            itemNumber={item.number}
            onChange={(v) => onSetMonitored([item.id], v)}
          />
        </div>
      </TableCell>
    </TableRow>
  );
});
