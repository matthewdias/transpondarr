import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Link } from "react-router";
import { ChevronLeft, Loader2, TriangleAlert } from "lucide-react";
import { api, ApiError, type Candidate, type MonitorItems } from "@/lib/api";
import { formatLabel, isUpcoming, statusLabel } from "@/lib/chart";
import { profilesQuery, titlesQuery, settingsQuery } from "@/lib/queries";
import { useIsMobile } from "@/hooks/use-mobile";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Drawer,
  DrawerContent,
  DrawerDescription,
  DrawerHeader,
  DrawerTitle,
} from "@/components/ui/drawer";

/** CandidateDTO satisfies this structurally; a SeasonEntryDTO has the same
 * next broadcast as next_episode, which its call site maps. */
export type AddTitle = Pick<
  Candidate,
  "provider" | "provider_id" | "episodes" | "status" | "format" | "next_item"
>;

type AddedTitle = Awaited<ReturnType<typeof api.addTitle>>;

// Before the add, not after: a new title sorts to the front of the search sweep queue
// and one pass grabs everything eligible.
const monitorChoices: { value: MonitorItems; label: string; hint: string }[] = [
  { value: "all", label: "All episodes", hint: "Including the back catalogue" },
  {
    value: "future",
    // Named for what it keys on — having aired — so the answer on a title with
    // no back catalogue, and on one that is all back catalogue, is derivable.
    label: "Only unaired",
    hint: "From the next broadcast onwards",
  },
];

/**
 * The set the stored cut will cover, in the words of the episodes.
 * Its branches mirror catalog.monitorCut's so the two agree by construction: the
 * status determines the cut there, so nothing here may re-derive an answer from it.
 */
function monitorSummary(
  target: AddTitle,
  mode: MonitorItems,
): { text: string; warn: boolean } {
  const count = target.episodes ?? 0;
  const next = target.next_item ?? 0;

  if (target.format === "MOVIE") {
    return { text: "The film will be monitored.", warn: false };
  }
  // A title that has not started takes this branch and no other: its control is
  // hidden, so the monitor mode it is summarising is always "all" (#217).
  if (mode === "all") {
    return {
      text:
        count > 1
          ? `All ${count} episodes will be monitored.`
          : count === 1
            ? "The only episode will be monitored."
            : "Every episode will be monitored.",
      warn: false,
    };
  }
  // The cut is the next broadcast, which covers items only while it lands
  // inside the run; an unknown count is a run long enough to contain it.
  if (next > 0 && (count === 0 || next <= count)) {
    return {
      text:
        count > 0
          ? `${count - next + 1} of ${count} episodes will be monitored, from episode ${next}.`
          : `Episodes from ${next} onwards will be monitored.`,
      warn: false,
    };
  }
  // Everything else falls past the last item — a finished run, one between
  // cours, and a schedule extending beyond the count alike.
  return {
    text:
      count > 1
        ? `None of the ${count} episodes will be monitored. Any episode added later will be.`
        : count === 1
          ? "The only episode will not be monitored. Any episode added later will be."
          : "No episode on record will be monitored. Any episode added later will be.",
    warn: true,
  };
}

/**
 * The add-time decisions for one title: which items to monitor and which
 * quality profile to score its releases against.
 */
export function AddTitleForm({
  title,
  target,
  onAdded,
  onExists,
  onBack,
}: {
  title: string;
  target: AddTitle;
  onAdded: (added: AddedTitle) => void;
  onExists?: () => void;
  onBack?: () => void;
}) {
  const [monitorItems, setMonitorItems] = useState<MonitorItems>("all");
  // A movie is one item, so all vs. future adds nothing to the title-level
  // Monitored switch.
  const isMovie = target.format === "MOVIE";
  // Both choices resolve to the same cut before anything has aired (#217), so
  // the control is hidden and only the summary below shows the result.
  const askMonitor = !isMovie && !isUpcoming(target.status);
  const summary = monitorSummary(target, monitorItems);
  const [profileId, setProfileId] = useState<number | null>(null);
  const queryClient = useQueryClient();
  const profiles = useQuery(profilesQuery());
  // Only a film's destination is in question here, so no request is sent for a series.
  const settings = useQuery({ ...settingsQuery(), enabled: isMovie });
  const noMoviesRoot = isMovie && settings.data?.library.movies_dir === "";

  const shown = profileId ?? profiles.data?.find((p) => p.is_default)?.id;
  const meta = [
    target.format && formatLabel(target.format),
    // AniList reports a film as one episode, which is not a fact about it.
    !isMovie && target.episodes ? `${target.episodes} ep` : null,
    target.status && statusLabel(target.status),
  ].filter(Boolean) as string[];

  const add = useMutation({
    mutationFn: () =>
      api.addTitle(target.provider, target.provider_id, {
        monitorItems,
        // Untouched sends nothing, so the server's default profile applies and
        // the common case stays one click.
        ...(profileId ? { qualityProfileId: profileId } : {}),
      }),
    onSuccess: (added) => {
      queryClient.invalidateQueries({ queryKey: titlesQuery().queryKey });
      toast.success("Title added", {
        description: `${added.title} — ${added.items.length} wanted ${
          added.items.length === 1 ? "item" : "items"
        } expanded`,
      });
      onAdded(added);
    },
    onError: (err) => {
      if (err instanceof ApiError && err.status === 409) {
        toast.info("Already tracking", { description: title });
        onExists?.();
        return;
      }
      toast.error("Could not add title", {
        description: err instanceof Error ? err.message : String(err),
      });
    },
  });

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        add.mutate();
      }}
    >
      <div className="space-y-3">
        {meta.length > 0 && (
          <p className="text-[12.5px] text-faint">{meta.join(" · ")}</p>
        )}

        {/* Told, not blocked: #198 and PR #57 both rule out blocking a manual add,
            and the grab row stays open until the root is set rather than failing. */}
        {noMoviesRoot && (
          <Link
            to="/settings"
            className="group flex items-start gap-2 rounded-md border border-dashed bg-panel-2/40 px-3 py-2 text-[12.5px] text-muted-foreground hover:text-foreground"
          >
            <TriangleAlert className="mt-0.5 size-3.5 flex-none text-dl" />
            <span>
              No movies directory is configured, so this film will wait in the
              queue instead of importing. Set one under{" "}
              <span className="underline underline-offset-2">
                Settings &rsaquo; Library
              </span>
              .
            </span>
          </Link>
        )}

        <div className="space-y-1">
          <span className="block text-xs font-medium text-muted-foreground">
            Monitor
          </span>
          {askMonitor && (
            <Select
              value={monitorItems}
              onValueChange={(v) => setMonitorItems(v as MonitorItems)}
            >
              <SelectTrigger
                aria-label="Monitor"
                aria-describedby="monitor-summary"
                className="w-full"
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {monitorChoices.map((c) => (
                  <SelectItem
                    key={c.value}
                    value={c.value}
                    description={c.hint}
                  >
                    {c.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
          {/* Outside the dropdown deliberately: the trigger shows the label
              alone, so a consequence only the open menu shows is unread. */}
          <p
            id="monitor-summary"
            className="flex items-start gap-1.5 text-[12.5px] text-muted-foreground"
          >
            {summary.warn && (
              <TriangleAlert className="mt-0.5 size-3.5 flex-none text-dl" />
            )}
            <span>{summary.text}</span>
          </p>
        </div>

        {/* The row is reserved but never blocking: the add omits the profile until
            one is picked, so a slow or failed fetch takes the server's
            default rather than disabling the button. */}
        {(profiles.isPending || profiles.isPaused) && (
          <div className="space-y-1">
            <span className="block text-xs font-medium text-muted-foreground">
              Quality profile
            </span>
            <Skeleton className="h-9 w-full rounded-md" />
          </div>
        )}

        {!!profiles.data?.length && shown !== undefined && (
          <div className="space-y-1">
            <span className="block text-xs font-medium text-muted-foreground">
              Quality profile
            </span>
            <Select
              value={String(shown)}
              onValueChange={(v) => setProfileId(Number(v))}
            >
              <SelectTrigger aria-label="Quality profile" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {profiles.data.map((p) => (
                  <SelectItem key={p.id} value={String(p.id)}>
                    {p.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        )}
      </div>

      {/* DialogFooter in a drawer too: below sm it stacks the primary action on
          top, which is the mobile container's shape. */}
      <DialogFooter className="mt-4">
        {onBack && (
          <Button
            type="button"
            variant="ghost"
            onClick={onBack}
            className="sm:mr-auto"
          >
            <ChevronLeft className="size-4" /> Back
          </Button>
        )}
        <Button
          type="submit"
          disabled={add.isPending}
          // The plain "Add" buttons behind the form share this visible label,
          // so the accessible name includes the title.
          aria-label={`Add ${title}`}
        >
          {add.isPending && <Loader2 className="size-3.5 animate-spin" />}
          Add
        </Button>
      </DialogFooter>
    </form>
  );
}

/** AddTitleForm in its own dialog (drawer on mobile), for callers with no
 * container of their own to host it. */
export function AddTitleDialog({
  title,
  target,
  open,
  onOpenChange,
  onAdded,
  onExists,
}: {
  title: string;
  target: AddTitle;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onAdded: (added: AddedTitle) => void;
  onExists?: () => void;
}) {
  const isMobile = useIsMobile();
  const description = "Choose what to monitor and how releases are scored.";
  // Mounting only while open reseeds the form per title, so a previous title's
  // choices can never persist.
  const form = open && (
    <AddTitleForm
      title={title}
      target={target}
      onAdded={onAdded}
      onExists={onExists}
    />
  );

  if (isMobile) {
    return (
      <Drawer open={open} onOpenChange={onOpenChange}>
        <DrawerContent className="px-4 pb-6">
          <DrawerHeader className="px-0">
            <DrawerTitle>{title}</DrawerTitle>
            <DrawerDescription>{description}</DrawerDescription>
          </DrawerHeader>
          {form}
        </DrawerContent>
      </Drawer>
    );
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle className="pr-6">{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        {form}
      </DialogContent>
    </Dialog>
  );
}
