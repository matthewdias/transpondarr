import { useEffect, useId, useState } from "react";
import { Link } from "react-router";
import {
  useInfiniteQuery,
  useMutation,
  useQueryClient,
} from "@tanstack/react-query";
import { toast } from "sonner";
import {
  CalendarClock,
  ChevronDown,
  ChevronRight,
  EyeOff,
  ListChecks,
  RefreshCw,
  Search,
  TriangleAlert,
} from "lucide-react";
import {
  api,
  ApiError,
  type CutoffGroup,
  type CutoffItem,
  type GlobalMissingReason,
  type MissingGroup,
  type MissingItem,
  type SeriesMissingReason,
} from "@/lib/api";
import { wantedCutoffQuery, wantedMissingQuery } from "@/lib/queries";
import { airDate, countdownOrDate, pad2, plural } from "@/lib/format";
import { searchQueuedToast } from "@/lib/search-queued-toast";
import { goalLine, ownGoals, sharedGoals } from "@/lib/unmet-goals";
import { cn } from "@/lib/utils";
import { ItemStatusBadge } from "@/components/badges";
import { Topbar } from "@/components/topbar";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";

type WantedTab = "missing" | "cutoff";

// The reason tiers' vocabulary (#150): the page says what blocks everything,
// a group header says where its series stands in the sweep queue, and a row
// speaks only when it has its own story. Tone separates "you have to do
// something" from "the queue is working".
const globalReasonText: Record<GlobalMissingReason, string> = {
  no_indexer:
    "No indexer is configured, so nothing here can be searched for. Add one under Settings.",
  automation_off:
    "Automation is off: nothing here will be grabbed on its own. Searches you trigger still run.",
  notify_only:
    "Automation is rehearsing: decisions are notified, but nothing reaches the download client.",
};

const seriesReasonLabel: Record<SeriesMissingReason, string> = {
  unmonitored: "Unmonitored",
  blocklisted: "Releases blocklisted",
  never_searched: "Not searched yet",
  search_backoff: "Search backing off",
  search_due: "Queued for search",
};

const seriesReasonTone: Record<SeriesMissingReason, string> = {
  unmonitored: "border-border bg-panel-2 text-faint",
  blocklisted: "border-dl/40 text-dl",
  never_searched: "border-border bg-panel-2 text-muted-foreground",
  search_backoff: "border-border bg-panel-2 text-muted-foreground",
  search_due: "border-border bg-panel-2 text-muted-foreground",
};

export function WantedPage() {
  const [tab, setTab] = useState<WantedTab>("missing");
  // One group of independent filters, so the state is the set that is on. The
  // two flags below are what the queries take; nothing else sees the set.
  const [scope, setScope] = useState<string[]>([]);
  const unaired = scope.includes("unaired");
  const unmonitored = scope.includes("unmonitored");
  const includeId = useId();

  return (
    <>
      <Topbar title="Wanted" />
      <div className="px-4 py-6 sm:px-6">
        <Tabs
          value={tab}
          onValueChange={(v) => setTab(v as WantedTab)}
          className="gap-0"
        >
          <div className="flex flex-wrap items-center justify-between gap-3">
            <TabsList>
              <TabsTrigger value="missing">Missing</TabsTrigger>
              <TabsTrigger value="cutoff">Cutoff Unmet</TabsTrigger>
            </TabsList>
            <div className="flex flex-wrap items-center gap-2">
              <span id={includeId} className="text-xs text-muted-foreground">
                Include
              </span>
              <ToggleGroup
                type="multiple"
                variant="chip"
                size="chip"
                value={scope}
                onValueChange={setScope}
                aria-labelledby={includeId}
              >
                {tab === "missing" && (
                  <ToggleGroupItem value="unaired">
                    <CalendarClock /> Unaired
                  </ToggleGroupItem>
                )}
                <ToggleGroupItem value="unmonitored">
                  <EyeOff /> Unmonitored
                </ToggleGroupItem>
              </ToggleGroup>
            </div>
          </div>

          <TabsContent value="missing" className="mt-4">
            <MissingTab unaired={unaired} unmonitored={unmonitored} />
          </TabsContent>
          <TabsContent value="cutoff" className="mt-4">
            <CutoffTab unmonitored={unmonitored} />
          </TabsContent>
        </Tabs>
      </div>
    </>
  );
}

function MissingTab({
  unaired,
  unmonitored,
}: {
  unaired: boolean;
  unmonitored: boolean;
}) {
  const {
    data,
    isLoading,
    isPaused,
    isError,
    error,
    refetch,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
  } = useInfiniteQuery(wantedMissingQuery(unaired, unmonitored));
  const groups = data?.pages.flatMap((p) => p.groups) ?? [];
  const globalReason = data?.pages[0]?.global_reason;
  const [selected, setSelected] = useState<Set<number>>(new Set());
  // Changing scope changes which series are listed at all, and a selection the
  // user can no longer see would still be queued by "Search selected".
  useEffect(() => setSelected(new Set()), [unaired, unmonitored]);

  const toggle = (seriesId: number) =>
    setSelected((prev) => {
      const next = new Set(prev);
      if (!next.delete(seriesId)) next.add(seriesId);
      return next;
    });

  if (isLoading || isPaused) return <ListSkeleton />;
  if (isError)
    return (
      <ListError what="the missing list" error={error} onRetry={refetch} />
    );

  return (
    <>
      {globalReason && (
        <div className="mb-3 flex items-center gap-2.5 rounded-lg border border-dl/30 bg-dl-weak/40 px-3.5 py-2.5">
          <TriangleAlert className="size-4 shrink-0 text-dl" />
          <p className="text-[13px] text-foreground/90">
            {globalReasonText[globalReason]}
          </p>
        </div>
      )}
      <SearchActions
        selectedSeries={[...selected]}
        onDone={() => setSelected(new Set())}
      />
      {groups.length === 0 ? (
        <EmptyState
          title="Nothing missing"
          blurb={
            unaired
              ? "Every monitored episode is either in the library or in flight."
              : "Every aired, monitored episode is either in the library or in flight. Turn on Unaired to see what is still to come."
          }
        />
      ) : (
        <div className="space-y-3">
          {groups.map((group) => (
            <MissingGroupCard
              key={group.series_id}
              group={group}
              selected={selected.has(group.series_id)}
              onToggle={() => toggle(group.series_id)}
            />
          ))}
        </div>
      )}
      <LoadMore
        hasNextPage={hasNextPage}
        isFetching={isFetchingNextPage}
        onClick={() => fetchNextPage()}
      />
    </>
  );
}

// GroupSection is the collapsible card both tabs' groups share. No
// overflow-hidden on the section -- it would become the sticky containing
// block and pin the header to the card instead of the viewport -- so the
// rounding is carried by the header and last row themselves. The header's
// background is the opaque panel token: rows scroll under it while stuck.
function GroupSection({
  title,
  header,
  subheader,
  children,
}: {
  title: string;
  header: React.ReactNode;
  subheader?: React.ReactNode;
  children: React.ReactNode;
}) {
  const [collapsed, setCollapsed] = useState(false);
  return (
    <section className="rounded-lg border bg-card shadow-sm [&>*:last-child]:rounded-b-lg">
      <header
        className={cn(
          // 49px is the sticky Topbar's height; group headers stack under it.
          "sticky top-[49px] z-[5] rounded-t-[7px] bg-panel-2 px-3.5 py-2.5",
          !collapsed && "border-b",
        )}
      >
        <div className="flex items-center gap-3">
          <button
            type="button"
            aria-expanded={!collapsed}
            aria-label={`${collapsed ? "Expand" : "Collapse"} ${title}`}
            onClick={() => setCollapsed((c) => !c)}
            className="grid size-5 shrink-0 place-items-center rounded text-muted-foreground hover:bg-panel-2 hover:text-foreground"
          >
            <ChevronDown
              className={cn(
                "size-4 transition-transform",
                collapsed && "-rotate-90",
              )}
            />
          </button>
          {header}
        </div>
        {subheader}
      </header>
      {!collapsed && children}
    </section>
  );
}

// overflowRow links to the series for what the group cap left out.
function OverflowRow({ seriesId, label }: { seriesId: number; label: string }) {
  return (
    <Link
      to={`/series/${seriesId}`}
      className="flex items-center justify-between px-3.5 py-2 text-xs hover:bg-panel-2/40"
    >
      <span className="text-faint">{label}</span>
      <span className="inline-flex items-center gap-1 font-medium text-primary">
        Go to series <ChevronRight className="size-3.5" />
      </span>
    </Link>
  );
}

function MissingGroupCard({
  group,
  selected,
  onToggle,
}: {
  group: MissingGroup;
  selected: boolean;
  onToggle: () => void;
}) {
  const hidden = group.missing - group.items.length;
  return (
    <GroupSection
      title={group.series_title}
      header={
        <>
          <input
            type="checkbox"
            checked={selected}
            onChange={onToggle}
            className="size-3.5 accent-primary"
            aria-label={`Select ${group.series_title}`}
          />
          <Link
            to={`/series/${group.series_id}`}
            className="min-w-0 flex-1 truncate text-sm font-semibold hover:underline"
          >
            {group.series_title}
          </Link>
          <span className="text-xs text-faint tabular-nums">
            {plural(group.missing, "episode")} missing
          </span>
          <SeriesReasonBadge group={group} />
        </>
      }
    >
      {group.items.map((item) => (
        <MissingRow key={item.id} seriesId={group.series_id} item={item} />
      ))}
      {hidden > 0 && (
        <OverflowRow
          seriesId={group.series_id}
          label={`${plural(hidden, "more episode")} not shown`}
        />
      )}
    </GroupSection>
  );
}

function MissingRow({
  seriesId,
  item,
}: {
  seriesId: number;
  item: MissingItem;
}) {
  return (
    <div className="flex items-center gap-3 border-b px-3.5 py-2 last:border-b-0 hover:bg-panel-2/40">
      <span className="w-8 shrink-0 text-right font-mono text-xs text-faint tabular-nums">
        {pad2(item.number)}
      </span>
      <span className="min-w-0 flex-1 truncate text-sm text-foreground/90">
        {item.name || `Episode ${item.number}`}
      </span>
      <span className="hidden w-28 shrink-0 text-right text-xs text-faint sm:block">
        {item.airs_at ? (
          airDate(item.airs_at)
        ) : (
          <span title="AniList publishes no broadcast time for this episode">
            No air date
          </span>
        )}
      </span>
      <ItemReasonBadge item={item} />
      <Button variant="outline" size="sm" asChild>
        {/* #105's episode-targeted search: the Releases tab opens filtered to
            this episode, where the unchanged manual grab lives. */}
        <Link to={`/series/${seriesId}?item=${item.number}`}>
          <Search className="size-4" /> Search
        </Link>
      </Button>
    </div>
  );
}

function SeriesReasonBadge({ group }: { group: MissingGroup }) {
  const detail =
    group.reason === "blocklisted"
      ? plural(group.blocked_releases ?? 0, "release")
      : group.reason === "search_backoff"
        ? `Next search ${countdownOrDate(group.next_search_at)}`
        : undefined;
  return (
    <span
      title={detail || undefined}
      className={cn(
        "hidden shrink-0 items-center rounded-full border px-2.5 py-0.5 text-[11.5px] font-semibold whitespace-nowrap md:inline-flex",
        seriesReasonTone[group.reason],
      )}
    >
      {seriesReasonLabel[group.reason]}
    </span>
  );
}

// A row speaks only when it has its own story; most rows are told by their
// group and stay quiet.
function ItemReasonBadge({ item }: { item: MissingItem }) {
  if (!item.reason) return null;
  return (
    <span
      title={item.reason === "grab_failed" ? item.reason_detail : undefined}
      className={cn(
        "hidden shrink-0 items-center rounded-full border px-2.5 py-0.5 text-[11.5px] font-semibold whitespace-nowrap md:inline-flex",
        item.reason === "grab_failed"
          ? "border-destructive/40 text-destructive"
          : "border-border bg-panel-2 text-faint",
      )}
    >
      {item.reason === "grab_failed" ? "Last grab failed" : "Not aired yet"}
    </span>
  );
}

function CutoffTab({ unmonitored }: { unmonitored: boolean }) {
  const {
    data,
    isLoading,
    isPaused,
    isError,
    error,
    refetch,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
  } = useInfiniteQuery(wantedCutoffQuery(unmonitored));
  const groups = data?.pages.flatMap((p) => p.groups) ?? [];

  if (isLoading || isPaused) return <ListSkeleton />;
  if (isError)
    return <ListError what="the cutoff list" error={error} onRetry={refetch} />;
  if (groups.length === 0) {
    return (
      <EmptyState
        title="Nothing below cutoff"
        blurb="Held episodes on a profile with upgrades enabled appear here while what holds them scores below that profile's cutoff."
      />
    );
  }

  return (
    <>
      <div className="space-y-3">
        {groups.map((group) => (
          <CutoffGroupCard key={group.series_id} group={group} />
        ))}
      </div>
      <LoadMore
        hasNextPage={hasNextPage}
        isFetching={isFetchingNextPage}
        onClick={() => fetchNextPage()}
      />
    </>
  );
}

function CutoffGroupCard({ group }: { group: CutoffGroup }) {
  // Goals every item shares are said once here; a row keeps only its own.
  const shared = sharedGoals(group.items);
  const hidden = group.below - group.items.length;
  return (
    <GroupSection
      title={group.series_title}
      header={
        <>
          <Link
            to={`/series/${group.series_id}`}
            className="min-w-0 flex-1 truncate text-sm font-semibold hover:underline"
          >
            {group.series_title}
          </Link>
          <span className="text-xs text-faint tabular-nums">
            {plural(group.below, "episode")} below cutoff
          </span>
          <span
            className="hidden shrink-0 items-center rounded-full border border-border bg-panel-2 px-2.5 py-0.5 text-[11.5px] font-semibold whitespace-nowrap text-muted-foreground md:inline-flex"
            title={`The ${group.profile_name} profile's cutoff`}
          >
            {group.profile_name} · cutoff {group.cutoff_score}
          </span>
        </>
      }
      subheader={
        shared.length > 0 && (
          <div className="mt-1 truncate pl-8 text-[11px] text-dl">
            Wanted: {goalLine(shared)}
          </div>
        )
      }
    >
      {group.items.map((item) => (
        <CutoffRow
          key={item.id}
          seriesId={group.series_id}
          cutoff={group.cutoff_score}
          item={item}
          shared={shared}
        />
      ))}
      {hidden > 0 && (
        <OverflowRow
          seriesId={group.series_id}
          label={`${plural(hidden, "more episode")} not shown`}
        />
      )}
    </GroupSection>
  );
}

function CutoffRow({
  seriesId,
  cutoff,
  item,
  shared,
}: {
  seriesId: number;
  cutoff: number;
  item: CutoffItem;
  shared: { label: string; points: number }[];
}) {
  const own = ownGoals(item, shared);
  return (
    <div className="flex items-center gap-3 border-b px-3.5 py-2 last:border-b-0 hover:bg-panel-2/40">
      <span className="w-8 shrink-0 text-right font-mono text-xs text-faint tabular-nums">
        {pad2(item.number)}
      </span>
      <div className="min-w-0 flex-1">
        <div className="truncate font-mono text-[12px] text-faint">
          {item.held_release}
        </div>
        {own.length > 0 ? (
          <div className="truncate text-[11px] text-dl">
            Also wants {goalLine(own)}
          </div>
        ) : (
          (item.unmet_goals?.length ?? 0) === 0 && (
            // Nothing the profile can describe is missing, yet it is still
            // below the cutoff: the cutoff is above this profile's own ceiling,
            // which is a profile-editor problem rather than this row's.
            <div
              className="truncate text-[11px] text-faint"
              title={`This profile's best possible score is ${item.score}, below its cutoff of ${cutoff}, so nothing can clear it`}
            >
              Nothing left to improve
            </div>
          )
        )}
      </div>
      <span className="hidden w-24 shrink-0 text-right text-xs text-muted-foreground tabular-nums sm:block">
        {item.score} / {cutoff}
      </span>
      <ItemStatusBadge status={item.status} />
      <Button variant="outline" size="sm" asChild>
        <Link to={`/series/${seriesId}?item=${item.number}`}>
          <Search className="size-4" /> Search
        </Link>
      </Button>
    </div>
  );
}

// A search is queued, never run here: seriesPerPass bounds how much of the
// indexer budget one pass can spend, so the toast says queued rather than done.
function SearchActions({
  selectedSeries,
  onDone,
}: {
  selectedSeries: number[];
  onDone: () => void;
}) {
  const queryClient = useQueryClient();
  const queue = useMutation({
    mutationFn: (seriesIds: number[]) => api.queueWantedSearch(seriesIds),
    onSuccess: (res) => {
      const { title, description } = searchQueuedToast(res);
      toast.success(title, { description });
      onDone();
      void queryClient.invalidateQueries({ queryKey: ["wanted"] });
    },
    onError: (err) =>
      toast.error(
        err instanceof ApiError ? err.message : "Could not queue the search",
      ),
  });

  return (
    <div className="mb-3 flex flex-wrap items-center gap-2">
      <Button
        variant="outline"
        size="sm"
        disabled={selectedSeries.length === 0 || queue.isPending}
        onClick={() => queue.mutate(selectedSeries)}
      >
        <Search className="size-4" />
        Search selected
        {selectedSeries.length > 0 && ` (${selectedSeries.length})`}
      </Button>
      <Button
        variant="ghost"
        size="sm"
        disabled={queue.isPending}
        onClick={() => queue.mutate([])}
      >
        <RefreshCw className="size-4" /> Search all
      </Button>
    </div>
  );
}

function LoadMore({
  hasNextPage,
  isFetching,
  onClick,
}: {
  hasNextPage: boolean;
  isFetching: boolean;
  onClick: () => void;
}) {
  if (!hasNextPage) return null;
  return (
    <Button
      variant="outline"
      size="sm"
      className="mt-3"
      disabled={isFetching}
      onClick={onClick}
    >
      {isFetching ? "Loading…" : "Load more"}
    </Button>
  );
}

function EmptyState({ title, blurb }: { title: string; blurb: string }) {
  return (
    <div className="mx-auto flex max-w-md flex-col items-center rounded-lg border border-dashed bg-card px-6 py-12 text-center">
      <ListChecks className="mb-3 size-6 text-faint" />
      <h3 className="text-sm font-semibold">{title}</h3>
      <p className="mt-1.5 text-sm text-muted-foreground">{blurb}</p>
    </div>
  );
}

function ListError({
  what,
  error,
  onRetry,
}: {
  what: string;
  error: unknown;
  onRetry: () => void;
}) {
  return (
    <div className="flex items-center gap-3 rounded-lg border border-dashed bg-card px-3.5 py-3">
      <TriangleAlert className="size-4 shrink-0 text-dl" />
      <p className="min-w-0 flex-1 text-[13px] text-muted-foreground">
        Couldn’t load {what}.{" "}
        {error instanceof ApiError ? error.message : String(error)}
      </p>
      <Button variant="outline" size="sm" onClick={onRetry}>
        <RefreshCw className="size-4" /> Try again
      </Button>
    </div>
  );
}

function ListSkeleton() {
  return (
    <div className="overflow-hidden rounded-lg border bg-card shadow-sm">
      {Array.from({ length: 4 }).map((_, i) => (
        <div
          key={i}
          className="flex items-center gap-3 border-b px-3.5 py-3 last:border-b-0"
        >
          <Skeleton className="size-3.5" />
          <Skeleton className="h-3.5 w-8" />
          <Skeleton className="h-3.5 flex-1" />
          <Skeleton className="h-5 w-24 rounded-full" />
        </div>
      ))}
    </div>
  );
}
