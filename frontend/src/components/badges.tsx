import {
  Check,
  Download,
  EyeOff,
  FolderClock,
  TriangleAlert,
} from "lucide-react";
import {
  useEffect,
  useRef,
  useState,
  type PointerEvent,
  type ReactNode,
} from "react";
import { cn } from "@/lib/utils";
import type { ItemStatus } from "@/lib/api";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";

const badgeBase =
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

// Closes whichever explanation is open, so opening another replaces it.
let closeOpenExplanation: (() => void) | null = null;

// A button instead of a title attribute, so touch and keyboard can open the
// explanation. A button inside a link is invalid, so `plain` keeps the attribute.
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
  const [open, setOpen] = useState(false);
  // Set when a click, tap or key opened it, so the pointer leaving doesn't close it.
  const pinned = useRef(false);
  const timer = useRef<number | undefined>(undefined);
  const close = useRef(() => {
    window.clearTimeout(timer.current);
    pinned.current = false;
    setOpen(false);
  }).current;
  useEffect(
    () => () => {
      window.clearTimeout(timer.current);
      if (closeOpenExplanation === close) closeOpenExplanation = null;
    },
    [close],
  );
  if (plain)
    return (
      <span className={cn(badgeBase, className)} title={explanation}>
        {children}
      </span>
    );
  const show = (next: boolean) => {
    if (next) {
      if (closeOpenExplanation !== close) closeOpenExplanation?.();
      closeOpenExplanation = close;
    } else if (closeOpenExplanation === close) {
      closeOpenExplanation = null;
    }
    setOpen(next);
  };
  const schedule = (next: boolean, ms: number) => {
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => show(next), ms);
  };
  // Mouse only: a tap's pointerover comes before its click, which would toggle the popover shut.
  const enter = (e: PointerEvent<HTMLElement>) => {
    if (e.pointerType === "mouse") schedule(true, 300);
  };
  const leave = (e: PointerEvent<HTMLElement>) => {
    if (e.pointerType === "mouse" && !pinned.current) schedule(false, 150);
  };
  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        window.clearTimeout(timer.current);
        pinned.current = next;
        show(next);
      }}
    >
      {/* A toggletip, not a dialog: focus stays on the badge and the status region announces the text. */}
      <PopoverTrigger
        aria-haspopup={undefined}
        aria-controls={undefined}
        onPointerEnter={enter}
        onPointerLeave={leave}
        onClick={(e) => {
          // Pin a hover-opened explanation instead of letting the click close it.
          if (open && !pinned.current) {
            e.preventDefault();
            pinned.current = true;
          }
        }}
        className={cn(
          badgeBase,
          "relative cursor-pointer outline-none focus-visible:ring-[3px] focus-visible:ring-ring",
          className,
        )}
      >
        {children}
      </PopoverTrigger>
      <span role="status">
        <PopoverContent
          portal={false}
          role={undefined}
          onPointerEnter={enter}
          onPointerLeave={leave}
          onOpenAutoFocus={(e) => e.preventDefault()}
          className="w-auto max-w-72 px-3 py-2 text-xs"
        >
          {explanation}
        </PopoverContent>
      </span>
    </Popover>
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
