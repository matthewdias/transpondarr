import { cn } from "@/lib/utils";
import type { ItemStatus } from "@/lib/api";
import { ItemStatusBadge, UnmonitoredItemBadge } from "@/components/badges";

// tracked is what the series is pursuing -- monitored and already broadcast --
// so a currently-airing show reads 3 / 3 rather than 3 / 12. The raw total rides
// along in a parenthetical, and is suppressed when the two agree.
export function LibraryProgress({
  format,
  inLibrary,
  tracked,
  monitored,
  total,
  status,
  importError,
}: {
  format: string;
  inLibrary: number;
  tracked: number;
  monitored: number;
  total: number;
  status?: ItemStatus;
  importError?: string;
}) {
  // Keyed on format alone (#208), which is what guarantees the film one item and
  // so makes the item's own state the row's: a one-episode OVA counts like the
  // series it is. A film with no state to report falls through to the count.
  if (format === "MOVIE" && status) {
    // Substituted, not qualified, as the detail page does it: every other status
    // stays true when unmonitored, and only "Wanted" turns into a false claim.
    return monitored === 0 && status === "wanted" ? (
      <UnmonitoredItemBadge />
    ) : (
      <ItemStatusBadge status={status} error={importError} movie />
    );
  }
  const pct = tracked > 0 ? (inLibrary / tracked) * 100 : 0;
  const complete = tracked > 0 && inLibrary >= tracked;
  // "0 / 0" would read as "this series has no episodes", which is exactly wrong
  // for a seasonal show added the week before it premieres. A zero denominator
  // has two causes, and naming the wrong one is a plain false statement.
  const empty = tracked === 0 && total > 0;
  const emptyLabel =
    monitored === 0 ? "Nothing monitored" : "Nothing aired yet";
  return (
    <div className="flex items-center gap-2.5 sm:min-w-[140px]">
      {/* the bar needs room; on mobile we keep just the count to avoid overflow.
          With a zero denominator it collapses to a few pixels beside the words. */}
      {!empty && (
        <div className="hidden h-1.5 flex-1 overflow-hidden rounded border border-border bg-panel-2 sm:block">
          <div
            className={cn("h-full", complete ? "bg-have" : "bg-primary")}
            style={{ width: `${pct}%` }}
          />
        </div>
      )}
      <span className="text-xs tabular-nums text-muted-foreground">
        {empty ? (
          <span className="whitespace-nowrap">{emptyLabel}</span>
        ) : (
          <>
            {inLibrary} / {tracked}
          </>
        )}
        {tracked !== total && (
          <span className="ml-1 whitespace-nowrap text-faint">
            ({total} total)
          </span>
        )}
      </span>
    </div>
  );
}
