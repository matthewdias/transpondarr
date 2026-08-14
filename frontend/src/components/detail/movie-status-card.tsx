import { Search } from "lucide-react";
import type { WantedItem } from "@/lib/api";
import { premiereLabel } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { ItemStatusBadge, UnmonitoredItemBadge } from "@/components/badges";
import { MonitorToggle } from "@/components/monitor-toggle";

/**
 * A film's acquisition state, standing in for the episodes table. Format is the
 * discriminator, so a one-item OVA never lands here.
 */
export function MovieStatusCard({
  item,
  onSearch,
  onSetMonitored,
}: {
  item: WantedItem;
  onSearch: () => void;
  onSetMonitored: (ids: number[], monitored: boolean) => void;
}) {
  const unmonitoredWanted = !item.monitored && item.status === "wanted";
  return (
    <div className="rounded-lg border bg-card shadow-sm">
      <div className="flex flex-col gap-3 px-4 py-3.5 sm:flex-row sm:items-center sm:gap-4">
        <div className="min-w-0 flex-1 space-y-2">
          <div className="flex flex-wrap items-center gap-2">
            {/* Substituted, not qualified: every other status stays true when
                unmonitored, and the toggle already shows the state. */}
            {unmonitoredWanted ? (
              <UnmonitoredItemBadge />
            ) : (
              <ItemStatusBadge
                status={item.status}
                error={item.import_error}
                movie
              />
            )}
            {item.airs_at && (
              <span className="text-[13px] text-muted-foreground">
                {premiereLabel(item.airs_at)}
              </span>
            )}
          </div>
          {item.release_title && (
            <p className="max-w-[560px] truncate font-mono text-xs text-muted-foreground">
              {item.release_title}
            </p>
          )}
        </div>
        <div className="flex items-center gap-2 sm:ml-auto">
          {/* Unconditional, like the episode row: nothing gates a manual path. */}
          <Button variant="outline" size="sm" onClick={onSearch}>
            <Search className="size-4" /> Search
          </Button>
          <MonitorToggle
            monitored={item.monitored}
            onChange={(v) => onSetMonitored([item.id], v)}
          />
        </div>
      </div>
    </div>
  );
}
