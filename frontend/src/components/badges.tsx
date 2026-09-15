import {
  Check,
  Download,
  EyeOff,
  FolderClock,
  TriangleAlert,
} from "lucide-react";
import type { ReactNode } from "react";
import { cn } from "@/lib/utils";
import type { ItemStatus } from "@/lib/api";
import { Toggletip } from "@/components/toggletip";

export const badgeBase =
  "inline-flex items-center gap-1 rounded-full border px-2.5 py-0.5 text-xs font-semibold whitespace-nowrap";

export function FormatBadge({ format }: { format: string }) {
  return (
    <span
      className={cn(
        badgeBase,
        "border-border bg-panel-2 text-muted-foreground",
      )}
    >
      {format}
    </span>
  );
}

export function MonitoredBadge({ monitored }: { monitored: boolean }) {
  return monitored ? (
    <span
      className={cn(
        badgeBase,
        "border-transparent bg-accent text-accent-foreground",
      )}
    >
      Monitored
    </span>
  ) : (
    <span className={cn(badgeBase, "border-border bg-panel-2 text-faint")}>
      Unmonitored
    </span>
  );
}

// Replaces "wanted" alone, at the render site: the other statuses stay true
// when unmonitored, and deriveItemState has no monitoring input.
export function UnmonitoredItemBadge() {
  return (
    <span className={cn(badgeBase, "border-border bg-panel-2 text-faint")}>
      <EyeOff className="size-3" /> Not monitored
    </span>
  );
}

// A link can't contain a button, so a link-wrapped badge passes `plain` for a title attribute.
function ExplainedBadge({
  className,
  explanation,
  plain,
  children,
}: {
  className: string;
  explanation: string;
  plain?: boolean;
  children: ReactNode;
}) {
  if (plain)
    return (
      <span className={cn(badgeBase, className)} title={explanation}>
        {children}
      </span>
    );
  return (
    <Toggletip explanation={explanation} className={cn(badgeBase, className)}>
      {children}
    </Toggletip>
  );
}

export function ItemStatusBadge({
  status,
  error,
  movie,
  plain,
}: {
  status: ItemStatus;
  error?: string;
  movie?: boolean;
  plain?: boolean;
}) {
  switch (status) {
    case "stuck":
      return (
        <ExplainedBadge
          plain={plain}
          className="border-destructive/40 bg-transparent text-destructive"
          explanation={
            error ||
            "The download finished but couldn’t be imported. The server log has the cause."
          }
        >
          <TriangleAlert className="size-3" /> Import blocked
        </ExplainedBadge>
      );
    case "in_library":
      return (
        <span
          className={cn(badgeBase, "border-transparent bg-have-weak text-have")}
        >
          <Check className="size-3" /> In library
        </span>
      );
    case "downloading":
      return (
        <span
          className={cn(badgeBase, "border-transparent bg-dl-weak text-dl")}
        >
          <Download className="size-3" /> Downloading
        </span>
      );
    case "deferred":
      // A film's deferral is a size tie or an unextracted archive (#210), never
      // a batch, so neither the label nor the single-episode advice applies.
      return movie ? (
        <ExplainedBadge
          plain={plain}
          className="border-dl/40 bg-transparent text-dl"
          explanation="The download finished but the film could not be picked out of it. The Activity queue shows what it needs."
        >
          <FolderClock className="size-3" /> Downloaded, not imported
        </ExplainedBadge>
      ) : (
        <ExplainedBadge
          plain={plain}
          className="border-dl/40 bg-transparent text-dl"
          explanation="A batch was downloaded but no single episode file could be imported. Grab a single-episode release to replace it."
        >
          <FolderClock className="size-3" /> Batch downloaded
        </ExplainedBadge>
      );
    default:
      return (
        <span
          className={cn(badgeBase, "border-border bg-transparent text-faint")}
        >
          Wanted
        </span>
      );
  }
}
