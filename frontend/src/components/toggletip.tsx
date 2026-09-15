import {
  useEffect,
  useRef,
  useState,
  type PointerEvent,
  type ReactNode,
} from "react";
import { cn } from "@/lib/utils";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";

// Closes whichever explanation is open, so opening another replaces it.
let closeOpenExplanation: (() => void) | null = null;

// A button instead of a title attribute, so touch and keyboard can open the
// explanation. Never render one inside a link: a button there is invalid.
export function Toggletip({
  className,
  explanation,
  children,
}: {
  className?: string;
  explanation: ReactNode;
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
      {/* A toggletip, not a dialog: focus stays on the trigger and the status region announces the text. */}
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
          "relative cursor-pointer outline-none focus-visible:ring-[3px] focus-visible:ring-ring/75 dark:focus-visible:ring-ring/50",
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
