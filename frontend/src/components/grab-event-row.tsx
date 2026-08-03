import { Check, CircleX, Download, FolderClock } from "lucide-react";
import { Link } from "react-router";
import { timeAgo } from "@/lib/format";
import { cn } from "@/lib/utils";
import {
  Item,
  ItemActions,
  ItemContent,
  ItemMedia,
} from "@/components/ui/item";

// The fields the row reads, shared by the per-series GrabEventDTO and the
// global ActivityEventDTO (which differ in hash spelling and series fields).
export interface PresentableGrabEvent {
  id: number;
  item_number: number;
  release_title: string;
  status: "grabbed" | "imported" | "import_deferred" | "failed";
  detail?: string;
  created_at: string;
}

// History is past-tense: a grabbed event is a recorded moment ("Grabbed"), never
// live progress — the queue and Episodes tab own in-flight state.
function presentGrabEvent(event: PresentableGrabEvent) {
  switch (event.status) {
    case "imported":
      return { verb: "Imported", icon: Check, tone: "bg-have-weak text-have" };
    case "import_deferred":
      return {
        verb: "Downloaded (batch)",
        icon: FolderClock,
        tone: "bg-panel-2 text-muted-foreground",
      };
    case "failed":
      return {
        verb: "Failed",
        icon: CircleX,
        tone: "bg-destructive/15 text-destructive",
      };
    default:
      return { verb: "Grabbed", icon: Download, tone: "bg-dl-weak text-dl" };
  }
}

export function GrabEventRow({
  event,
  series,
}: {
  event: PresentableGrabEvent;
  series?: { id: number; title: string };
}) {
  const { verb, icon: Icon, tone } = presentGrabEvent(event);
  return (
    <Item className="gap-3">
      <ItemMedia>
        <span className={cn("grid size-8 place-items-center rounded-lg", tone)}>
          <Icon className="size-4" />
        </span>
      </ItemMedia>
      <ItemContent className="min-w-0 gap-0.5">
        <div className="text-sm font-medium">
          {verb} · Episode {event.item_number}
          {series && (
            <>
              {" · "}
              <Link
                to={`/series/${series.id}`}
                className="font-normal text-muted-foreground hover:text-foreground hover:underline"
              >
                {series.title}
              </Link>
            </>
          )}
        </div>
        <div className="line-clamp-1 font-mono text-[12px] text-faint">
          {event.release_title}
        </div>
        {event.status === "failed" && event.detail && (
          <div className="line-clamp-2 text-[12px] text-destructive">
            {event.detail}
          </div>
        )}
      </ItemContent>
      <ItemActions>
        <span className="text-xs text-faint">{timeAgo(event.created_at)}</span>
      </ItemActions>
    </Item>
  );
}
