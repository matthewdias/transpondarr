import { cn } from "@/lib/utils";

// tracked is what the series is pursuing -- monitored and already broadcast --
// so a currently-airing show reads 3 / 3 rather than 3 / 12. The raw total rides
// along in a parenthetical, and is suppressed when the two agree.
export function LibraryProgress({
  inLibrary,
  tracked,
  total,
}: {
  inLibrary: number;
  tracked: number;
  total: number;
}) {
  const pct = tracked > 0 ? (inLibrary / tracked) * 100 : 0;
  const complete = tracked > 0 && inLibrary >= tracked;
  return (
    <div className="flex items-center gap-2.5 sm:min-w-[140px]">
      {/* the bar needs room; on mobile we keep just the count to avoid overflow */}
      <div className="hidden h-1.5 flex-1 overflow-hidden rounded border border-border bg-panel-2 sm:block">
        <div
          className={cn("h-full", complete ? "bg-have" : "bg-primary")}
          style={{ width: `${pct}%` }}
        />
      </div>
      <span className="text-xs tabular-nums text-muted-foreground">
        {inLibrary} / {tracked}
        {tracked !== total && (
          <span className="ml-1 text-faint">({total} total)</span>
        )}
      </span>
    </div>
  );
}
