import { useEffect, useId, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Pin, TriangleAlert } from "lucide-react";
import {
  api,
  ApiError,
  type AutomationMode,
  type SeriesDetail,
} from "@/lib/api";
import { statusLabel } from "@/lib/chart";
import {
  profilesQuery,
  releasesQuery,
  seriesDetailQuery,
  seriesQuery,
  settingsQuery,
} from "@/lib/queries";
import { cn } from "@/lib/utils";
import { AniListLink } from "@/components/anilist-link";
import { Topbar } from "@/components/topbar";
import { Poster } from "@/components/poster";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { DeleteSeriesDialog } from "@/components/detail/delete-series-dialog";
import { EpisodesTab } from "@/components/detail/episodes-tab";
import { ReleasesTab } from "@/components/detail/releases-tab";
import { HistoryTab } from "@/components/detail/history-tab";

type TabKey = "episodes" | "releases" | "history";

export function SeriesDetailPage() {
  const params = useParams();
  const id = Number(params.id);
  // ?item=N is how another page asks for an episode-targeted search (#150's
  // Wanted rows); the Episodes tab's own button sets the same state directly.
  const [search] = useSearchParams();
  const linkedItem = Number(search.get("item")) || null;
  const [tab, setTab] = useState<TabKey>(linkedItem ? "releases" : "episodes");
  // Radix unmounts an inactive panel, so the focused episode is the page's to
  // hold, not the Releases tab's.
  const [focusItem, setFocusItem] = useState<number | null>(linkedItem);
  // The page survives a series-to-series navigation, and an episode number from
  // the series you left means something else in the one you arrived at.
  useEffect(() => {
    setFocusItem(linkedItem);
    if (linkedItem) setTab("releases");
  }, [id, linkedItem]);
  const queryClient = useQueryClient();
  const navigate = useNavigate();

  const detailKey = seriesDetailQuery(id).queryKey;

  const {
    data: detail,
    isLoading,
    isError,
    error,
  } = useQuery({
    ...seriesDetailQuery(id),
    enabled: Number.isFinite(id),
  });

  // Monitoring only means anything while automation is on, so the header needs
  // the global switch. Assumed on until the settings load, so a slow fetch does
  // not flash a warning at someone whose automation is fine.
  const { data: settings } = useQuery(settingsQuery());
  const automationMode = settings?.automation.mode ?? "on";

  const monitor = useMutation({
    mutationFn: (v: boolean) => api.setMonitored(id, v),
    onMutate: async (v) => {
      await queryClient.cancelQueries({ queryKey: detailKey });
      const prev = queryClient.getQueryData(detailKey);
      queryClient.setQueryData(detailKey, (old) =>
        old ? { ...old, monitored: v } : old,
      );
      return { prev };
    },
    onError: (_e, _v, ctx) => {
      if (ctx?.prev) queryClient.setQueryData(detailKey, ctx.prev);
      toast.error("Could not update monitoring");
    },
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: detailKey });
      queryClient.invalidateQueries({ queryKey: seriesQuery().queryKey });
    },
  });

  // Selection is the page's, not the tab's: Radix unmounts an inactive panel,
  // so a selection held in the tab would evaporate on a round trip to Releases.
  const [selected, setSelected] = useState<Set<number>>(new Set());
  useEffect(() => setSelected(new Set()), [id]);
  const toggleSelect = (itemId: number) =>
    setSelected((prev) => {
      const next = new Set(prev);
      if (!next.delete(itemId)) next.add(itemId);
      return next;
    });

  const setItemsMonitored = useMutation({
    mutationFn: ({ ids, monitored }: { ids: number[]; monitored: boolean }) =>
      api.setItemsMonitored(ids, monitored),
    onMutate: async ({ ids, monitored }) => {
      await queryClient.cancelQueries({ queryKey: detailKey });
      const prev = queryClient.getQueryData(detailKey);
      const set = new Set(ids);
      queryClient.setQueryData(detailKey, (old) =>
        old
          ? {
              ...old,
              items: old.items.map((it) =>
                set.has(it.id) ? { ...it, monitored } : it,
              ),
            }
          : old,
      );
      return { prev };
    },
    onError: (_e, _v, ctx) => {
      if (ctx?.prev) queryClient.setQueryData(detailKey, ctx.prev);
      toast.error("Could not update episode monitoring");
    },
    onSuccess: () => setSelected(new Set()),
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: detailKey });
      queryClient.invalidateQueries({ queryKey: seriesQuery().queryKey });
    },
  });

  const notFound = isError && error instanceof ApiError && error.status === 404;

  const breadcrumb = (
    <div className="flex min-w-0 items-center gap-2">
      <Link to="/" className="font-medium text-faint hover:text-foreground">
        Series
      </Link>
      <span className="text-faint">/</span>
      <h1 className="truncate text-base font-semibold tracking-tight">
        {detail?.title ?? (notFound ? "Not found" : "…")}
      </h1>
    </div>
  );

  const searchAll = () => {
    setFocusItem(null);
    setTab("releases");
  };
  const searchItem = (n: number) => {
    setFocusItem(n);
    setTab("releases");
  };

  return (
    <>
      <Topbar
        breadcrumb={breadcrumb}
        actions={
          detail && (
            <DeleteSeriesDialog
              detail={detail}
              onDeleted={() => navigate("/", { replace: true })}
            />
          )
        }
      />
      <div className="px-4 py-6 sm:px-6">
        {isLoading && <HeaderSkeleton />}

        {notFound && (
          <div className="rounded-lg border border-dashed bg-card px-6 py-16 text-center">
            <h2 className="text-base font-semibold">Series not found</h2>
            <p className="mt-2 text-sm text-muted-foreground">
              It may have been removed.{" "}
              <Link to="/" className="text-accent-foreground hover:underline">
                Back to series
              </Link>
              .
            </p>
          </div>
        )}

        {isError && !notFound && (
          <div className="rounded-lg border border-destructive/40 bg-destructive/5 px-4 py-3 text-sm text-destructive">
            Failed to load series:{" "}
            {error instanceof Error ? error.message : String(error)}
          </div>
        )}

        {detail && (
          <>
            <DetailHeader
              detail={detail}
              automationMode={automationMode}
              onToggleMonitored={(v) => monitor.mutate(v)}
            />

            <Tabs
              value={tab}
              // Radix fires this only on a user-driven change, which is exactly
              // the seam: clicking the tab is the series-wide intent.
              onValueChange={(v) => {
                setFocusItem(null);
                setTab(v as TabKey);
              }}
              className="mt-1 gap-0"
            >
              <TabsList
                variant="line"
                className="mb-[18px] h-auto w-full justify-start gap-0.5 rounded-none border-b bg-transparent p-0"
              >
                <DetailTab
                  value="episodes"
                  label="Episodes"
                  count={detail.items.length}
                  active={tab === "episodes"}
                />
                <DetailTab
                  value="releases"
                  label="Releases"
                  active={tab === "releases"}
                />
                <DetailTab
                  value="history"
                  label="History"
                  active={tab === "history"}
                />
              </TabsList>

              <TabsContent value="episodes">
                <EpisodesTab
                  detail={detail}
                  onSearchAll={searchAll}
                  onSearchItem={searchItem}
                  selected={selected}
                  onToggleSelect={toggleSelect}
                  onSetMonitored={(ids, monitored) =>
                    setItemsMonitored.mutate({ ids, monitored })
                  }
                />
              </TabsContent>
              <TabsContent value="releases">
                <ReleasesTab
                  seriesId={id}
                  active={tab === "releases"}
                  focusItem={focusItem}
                  onClearFocus={() => setFocusItem(null)}
                />
              </TabsContent>
              <TabsContent value="history">
                <HistoryTab seriesId={id} active={tab === "history"} />
              </TabsContent>
            </Tabs>
          </>
        )}
      </div>
    </>
  );
}

// ProfilePicker sits in the chips row: the profile is context you glance at and
// occasionally change, not a form you fill in.
function ProfilePicker({ detail }: { detail: SeriesDetail }) {
  const queryClient = useQueryClient();
  const profiles = useQuery(profilesQuery());
  const assign = useMutation({
    mutationFn: (profileId: number) =>
      api.assignSeriesProfile(detail.id, profileId),
    onSuccess: (_res, profileId) => {
      const name = profiles.data?.find((p) => p.id === profileId)?.name;
      toast.success(name ? `Profile set to “${name}”` : "Profile updated");
      queryClient.invalidateQueries({
        queryKey: seriesDetailQuery(detail.id).queryKey,
      });
      queryClient.invalidateQueries({ queryKey: profilesQuery().queryKey });
    },
    onError: (e) =>
      toast.error("Failed to set profile", {
        description: e instanceof Error ? e.message : String(e),
      }),
  });

  if (!profiles.data?.length) return null;
  return (
    <Select
      value={String(detail.quality_profile_id)}
      onValueChange={(v) => assign.mutate(Number(v))}
      disabled={assign.isPending}
    >
      <SelectTrigger
        size="sm"
        aria-label="Quality profile"
        // The chip look: data-[size=sm]:h and the dark: variants must be
        // re-overridden here or the base trigger's win on specificity.
        className="h-[26px] gap-1.5 rounded-md border-border bg-panel-2 px-2.5 text-xs font-medium text-muted-foreground shadow-none data-[size=sm]:h-[26px] dark:bg-panel-2 dark:hover:bg-panel-2"
      >
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
  );
}

// PinnedGroupChip is the per-series "this group is definitive" knob (#61): free
// text because the pinned group need not be in the profile's ranked list.
export function PinnedGroupChip({ detail }: { detail: SeriesDetail }) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [group, setGroup] = useState("");
  const [delay, setDelay] = useState("");
  const groupId = useId();
  const delayId = useId();
  const delayHintId = useId();
  const current = detail.pinned_group ?? "";
  const currentDelay =
    detail.pin_delay_hours === undefined ? "" : String(detail.pin_delay_hours);
  // An explicit 0 is a real setting ("do not wait"), not the absent default.
  const delaySuffix =
    detail.pin_delay_hours === undefined
      ? ""
      : detail.pin_delay_hours === 0
        ? " · no wait"
        : ` · ${detail.pin_delay_hours}h`;

  const pin = useMutation({
    mutationFn: ({ g, d }: { g: string; d?: number }) =>
      api.setSeriesPinnedGroup(detail.id, g, d),
    onSuccess: (_res, { g }) => {
      toast.success(g.trim() ? `Pinned “${g.trim()}”` : "Pin cleared");
      queryClient.invalidateQueries({
        queryKey: seriesDetailQuery(detail.id).queryKey,
      });
      queryClient.invalidateQueries({
        queryKey: releasesQuery(detail.id).queryKey,
      });
      setOpen(false);
    },
    onError: (e) =>
      toast.error("Failed to update pinned group", {
        description: e instanceof Error ? e.message : String(e),
      }),
  });

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (o) {
          setGroup(current);
          setDelay(currentDelay);
        }
      }}
    >
      <DialogTrigger asChild>
        <button
          type="button"
          className="inline-flex items-center gap-1.5 rounded-md border border-border bg-panel-2 px-2.5 py-1 text-xs font-medium text-muted-foreground hover:text-accent-foreground"
        >
          <Pin className="size-3" aria-hidden />
          {current ? `Pin: ${current}${delaySuffix}` : "Pin group"}
        </button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>Pinned release group</DialogTitle>
          <DialogDescription>
            This group&apos;s eligible releases always rank first for this
            series, above profile scoring.
          </DialogDescription>
        </DialogHeader>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            // A cleared group takes its wait with it, so the disabled field's
            // leftover value must not ride along to be silently dropped.
            const blank = group.trim() === "" || delay === "";
            pin.mutate({ g: group, d: blank ? undefined : Number(delay) });
          }}
        >
          <div className="space-y-3">
            <div className="space-y-1">
              <label
                htmlFor={groupId}
                className="block text-xs font-medium text-muted-foreground"
              >
                Release group
              </label>
              <Input
                id={groupId}
                value={group}
                onChange={(e) => setGroup(e.target.value)}
                placeholder="e.g. ShinySubs"
                maxLength={100}
              />
            </div>
            <div className="space-y-1">
              <label
                htmlFor={delayId}
                className="block text-xs font-medium text-muted-foreground"
              >
                Wait for this group (hours)
              </label>
              <Input
                id={delayId}
                aria-describedby={delayHintId}
                type="number"
                min={0}
                max={8760}
                step={1}
                // The server drops a delay with no group to wait for, so the
                // field must not take input the save would silently discard.
                disabled={group.trim() === ""}
                value={delay}
                onChange={(e) => setDelay(e.target.value)}
                placeholder="Global default"
              />
              <p id={delayHintId} className="text-xs text-muted-foreground">
                How many hours automatic searches wait for this group after an
                episode airs. Blank uses the global default; 0 never waits.
              </p>
            </div>
          </div>
          <DialogFooter className="mt-4">
            {current && (
              <Button
                type="button"
                variant="outline"
                disabled={pin.isPending}
                onClick={() => pin.mutate({ g: "" })}
              >
                Clear
              </Button>
            )}
            <Button
              type="submit"
              disabled={
                pin.isPending ||
                (group.trim() === current && delay === currentDelay)
              }
            >
              Save
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/**
 * Monitoring switch. Monitored now means "will be searched and grabbed
 * automatically", so the global kill switch turns that label into a promise the
 * daemon is not keeping — the only case worth annotating, since an unmonitored
 * series is already saying it will not run.
 */
export function MonitoringToggle({
  monitored,
  automationMode,
  onToggle,
}: {
  monitored: boolean;
  automationMode: AutomationMode;
  onToggle: (v: boolean) => void;
}) {
  const note =
    automationMode === "off"
      ? {
          label: "Automation is off",
          title:
            "Nothing is searched or grabbed automatically until automation is enabled in Settings",
        }
      : automationMode === "notify_only"
        ? {
            label: "Notify-only rehearsal",
            title:
              "Automation reports what it would have grabbed but downloads nothing until it is switched on in Settings",
          }
        : null;
  return (
    <div className="flex flex-none flex-col items-end gap-1">
      <label className="flex cursor-pointer items-center gap-2.5 text-[13.5px] font-medium">
        <span
          className={monitored ? "text-foreground" : "text-muted-foreground"}
        >
          {monitored ? "Monitored" : "Unmonitored"}
        </span>
        <Switch
          checked={monitored}
          onCheckedChange={onToggle}
          aria-label="Toggle monitoring"
        />
      </label>
      {monitored && note && (
        // Helper text, not a chip: a bordered pill directly under a switch reads
        // as a second control. The icon carries the caution, since the palette's
        // amber is under 4.5:1 as text at this size.
        <Link
          to="/settings"
          className="group flex items-center gap-1.5 whitespace-nowrap text-[11px] text-muted-foreground hover:text-foreground"
          title={note.title}
        >
          <TriangleAlert className="size-3 flex-none text-dl" />
          <span>
            {note.label} —{" "}
            <span className="underline underline-offset-2 group-hover:decoration-current">
              Settings
            </span>
          </span>
        </Link>
      )}
    </div>
  );
}

function DetailHeader({
  detail,
  automationMode,
  onToggleMonitored,
}: {
  detail: SeriesDetail;
  automationMode: AutomationMode;
  onToggleMonitored: (v: boolean) => void;
}) {
  // English only, matching the discovery detail view.
  const subtitle = detail.english !== detail.title ? detail.english : null;
  const chips = [
    detail.format,
    `${detail.items.length} episodes`,
    detail.status ? statusLabel(detail.status) : null,
  ].filter(Boolean) as string[];

  return (
    <div className="mb-5 flex items-start gap-4 sm:gap-5">
      <Poster
        title={detail.title}
        coverUrl={detail.cover_url}
        size="lg"
        className="hidden sm:grid"
      />
      <div className="min-w-0 flex-1">
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0">
            <h1 className="text-xl font-semibold tracking-tight sm:text-2xl">
              {detail.title}
            </h1>
            {subtitle && (
              <p className="mt-0.5 text-sm text-faint">{subtitle}</p>
            )}
          </div>
          <MonitoringToggle
            monitored={detail.monitored}
            automationMode={automationMode}
            onToggle={onToggleMonitored}
          />
        </div>

        <div className="mt-3 flex flex-wrap items-center gap-2">
          {chips.map((c) => (
            <span
              key={c}
              className="inline-flex items-center rounded-md border border-border bg-panel-2 px-2.5 py-1 text-xs font-medium text-muted-foreground"
            >
              {c}
            </span>
          ))}
          {/* The link is only meaningful in AniList's id space. */}
          {detail.provider === "anilist" && detail.provider_id ? (
            <AniListLink
              id={detail.provider_id}
              className="inline-flex items-center rounded-md border border-border bg-panel-2 px-2.5 py-1 font-mono text-[11.5px] font-medium text-muted-foreground hover:text-accent-foreground"
            >
              AniList {detail.provider_id}
            </AniListLink>
          ) : null}
          <ProfilePicker detail={detail} />
          <PinnedGroupChip detail={detail} />
        </div>
      </div>
    </div>
  );
}

function DetailTab({
  value,
  label,
  count,
  active,
}: {
  value: TabKey;
  label: string;
  count?: number;
  active: boolean;
}) {
  return (
    <TabsTrigger
      value={value}
      className="flex-none gap-1.5 rounded-none border-0 border-b-2 border-transparent px-3.5 py-2.5 text-sm font-medium text-muted-foreground after:hidden data-[state=active]:border-b-primary data-[state=active]:bg-transparent data-[state=active]:text-foreground data-[state=active]:shadow-none"
    >
      {label}
      {count != null && (
        <span
          className={cn(
            "rounded-full border px-1.5 text-[11px] tabular-nums",
            active
              ? "border-transparent bg-accent text-accent-foreground"
              : "border-border bg-background text-faint",
          )}
        >
          {count}
        </span>
      )}
    </TabsTrigger>
  );
}

function HeaderSkeleton() {
  return (
    <div className="mb-5 flex items-start gap-5">
      <Skeleton className="hidden h-[116px] w-[82px] rounded-lg sm:block" />
      <div className="flex-1 space-y-3">
        <Skeleton className="h-7 w-64" />
        <Skeleton className="h-4 w-80" />
        <div className="flex gap-2 pt-1">
          <Skeleton className="h-6 w-14 rounded-md" />
          <Skeleton className="h-6 w-24 rounded-md" />
          <Skeleton className="h-6 w-20 rounded-md" />
        </div>
      </div>
    </div>
  );
}
