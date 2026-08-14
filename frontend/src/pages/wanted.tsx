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
  PartialBatchError,
  type CutoffGroup,
  type CutoffItem,
  type GlobalMissingReason,
  type ItemMissingReason,
  type MissingGroup,
  type MissingItem,
  type TitleMissingReason,
} from "@/lib/api";
import { wantedCutoffQuery, wantedMissingQuery } from "@/lib/queries";
import {
  airDate,
  countdownOrDate,
  pad2,
  plural,
  premiereDate,
  timeAgo,
} from "@/lib/format";
import { searchQueuedToast } from "@/lib/search-queued-toast";
import { goalLine, ownGoals, sharedGoals } from "@/lib/unmet-goals";
import { cn } from "@/lib/utils";
import { ItemStatusBadge } from "@/components/badges";
import { MonitorToggle } from "@/components/monitor-toggle";
import { Checkbox } from "@/components/ui/checkbox";
import { Topbar } from "@/components/topbar";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";

type WantedTab = "missing" | "cutoff";

// Format alone (#208), never item count: a one-episode OVA is a series here.
const isFilm = (format: string) => format === "MOVIE";

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

const titleReasonLabel: Record<TitleMissingReason, string> = {
  unmonitored: "Unmonitored",
  blocklisted: "Releases blocklisted",
  never_searched: "Not searched yet",
  search_backoff: "Search backing off",
  search_due: "Queued for search",
};

const titleReasonTone: Record<TitleMissingReason, string> = {
  unmonitored: "border-border bg-panel-2 text-faint",
  blocklisted: "border-dl/40 text-dl",
  never_searched: "border-border bg-panel-2 text-muted-foreground",
  search_backoff: "border-border bg-panel-2 text-muted-foreground",
  search_due: "border-border bg-panel-2 text-muted-foreground",
};

// The row tier. The last five are #181's: what the last pass decided about this
// episode, which is the half the user can act on.
const itemReasonLabel: Record<ItemMissingReason, string> = {
  unmonitored: "Not monitored",
  unaired: "Not aired yet",
  grab_failed: "Last grab failed",
  no_match: "Nothing matched",
  declined: "Releases declined",
  pin_held: "Waiting for the pinned group",
  would_grab: "Would have grabbed",
  add_failed: "Download client refused it",
};

// A refused add is a failure like a failed grab; a decline is the mid tone,
// because there is a setting behind it to change.
const itemReasonTone: Record<ItemMissingReason, string> = {
  unmonitored: "border-border bg-panel-2 text-faint",
  unaired: "border-border bg-panel-2 text-faint",
  grab_failed: "border-destructive/40 text-destructive",
  add_failed: "border-destructive/40 text-destructive",
  declined: "border-dl/40 text-dl",
  no_match: "border-border bg-panel-2 text-faint",
  pin_held: "border-border bg-panel-2 text-faint",
  would_grab: "border-border bg-panel-2 text-faint",
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

  const toggle = (titleId: number) =>
    setSelected((prev) => {
      const next = new Set(prev);
      if (!next.delete(titleId)) next.add(titleId);
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
        selectedTitles={[...selected]}
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
              key={group.title_id}
              group={group}
              selected={selected.has(group.title_id)}
              onToggle={() => toggle(group.title_id)}
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

// Invalidates rather than patching a page: unmonitoring removes the row from
// the default view entirely, which no in-place edit could express.
function useSetItemMonitored() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, monitored }: { id: number; monitored: boolean }) =>
      api.setItemsMonitored([id], monitored),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["wanted"] }),
    onError: (err) =>
      toast.error(
        err instanceof ApiError || err instanceof PartialBatchError
          ? err.message
          : "Could not update monitoring",
      ),
  });
}

function RowMonitorToggle({
  itemId,
  number,
  monitored,
}: {
  itemId: number;
  number: number;
  monitored: boolean;
}) {
  const set = useSetItemMonitored();
  return (
    <MonitorToggle
      monitored={monitored}
      itemNumber={number}
      disabled={set.isPending}
      onChange={(v) => set.mutate({ id: itemId, monitored: v })}
    />
  );
}

// overflowRow links to the series for what the group cap left out.
function OverflowRow({ titleId, label }: { titleId: number; label: string }) {
  return (
    <Link
      to={`/titles/${titleId}`}
      className="flex items-center justify-between px-3.5 py-2 text-xs hover:bg-panel-2/40"
    >
      <span className="text-faint">{label}</span>
      <span className="inline-flex items-center gap-1 font-medium text-primary">
        Go to title <ChevronRight className="size-3.5" />
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
  // Only split a whole group: with items paged the loaded rows are a sample, so
  // counting them would understate what is switched off.
  const unmonitored =
    hidden === 0 ? group.items.filter((i) => !i.monitored).length : 0;
  return (
    <GroupSection
      title={group.title}
      header={
        <>
          <Checkbox
            checked={selected}
            onCheckedChange={onToggle}
            aria-label={`Select ${group.title}`}
          />
          <Link
            to={`/titles/${group.title_id}`}
            className="min-w-0 flex-1 truncate text-sm font-semibold hover:underline"
          >
            {group.title}
          </Link>
          <span className="text-xs text-faint tabular-nums">
            {unmonitored > 0
              ? `${group.missing - unmonitored} missing · ${unmonitored} not monitored`
              : isFilm(group.format)
                ? "Missing"
                : `${plural(group.missing, "episode")} missing`}
          </span>
          <TitleReasonBadge group={group} />
        </>
      }
    >
      {group.items.map((item) => (
        <MissingRow
          key={item.id}
          titleId={group.title_id}
          format={group.format}
          item={item}
        />
      ))}
      {hidden > 0 && (
        <OverflowRow
          titleId={group.title_id}
          label={`${plural(hidden, "more episode")} not shown`}
        />
      )}
    </GroupSection>
  );
}

function MissingRow({
  titleId,
  format,
  item,
}: {
  titleId: number;
  format: string;
  item: MissingItem;
}) {
  const film = isFilm(format);
  return (
    <div className="flex items-center gap-3 border-b px-3.5 py-2 last:border-b-0 hover:bg-panel-2/40">
      <span className="w-8 shrink-0 text-right font-mono text-xs text-faint tabular-nums">
        {pad2(item.number)}
      </span>
      <span className="min-w-0 flex-1 truncate text-sm text-foreground/90">
        {item.name || (film ? "Film" : `Episode ${item.number}`)}
      </span>
      <span className="hidden w-28 shrink-0 text-right text-xs text-faint sm:block">
        {item.airs_at ? (
          film ? (
            premiereDate(item.airs_at)
          ) : (
            airDate(item.airs_at)
          )
        ) : (
          <span
            title={
              film
                ? "AniList publishes no release date for this film"
                : "AniList publishes no broadcast time for this episode"
            }
          >
            {film ? "No release date" : "No air date"}
          </span>
        )}
      </span>
      <ItemReasonBadge item={item} film={film} />
      <Button variant="outline" size="sm" asChild>
        {/* #105's episode-targeted search: the Releases tab opens filtered to
            this episode, where the unchanged manual grab lives. */}
        <Link to={`/titles/${titleId}?item=${item.number}`}>
          <Search className="size-4" /> Search
        </Link>
      </Button>
      <RowMonitorToggle
        itemId={item.id}
        number={item.number}
        monitored={item.monitored}
      />
    </div>
  );
}

function TitleReasonBadge({ group }: { group: MissingGroup }) {
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
        titleReasonTone[group.reason],
      )}
    >
      {titleReasonLabel[group.reason]}
    </span>
  );
}

// The pass tier is the only reason on this page that can go stale, so it is
// always shown with its age: a past-tense verb next to "2h ago" cannot read as
// a fact about now. The tooltip carries what the pass acted on.
function itemReasonTitle(item: MissingItem): string | undefined {
  const pass = item.last_pass;
  if (!pass) {
    return item.reason === "grab_failed" ? item.reason_detail : undefined;
  }
  return [
    pass.release_title,
    item.reason_detail,
    // countdownOrDate returns "in 4h" or a date, so the phrasing has to read
    // with both -- "Held until in 4h" is what a bare "until" gives you.
    item.reason === "pin_held" && pass.held_until
      ? `Grabbable ${countdownOrDate(pass.held_until)}`
      : undefined,
    pass.source === "feed"
      ? "Decided by a feed poll"
      : "Decided by a search of this title",
  ]
    .filter(Boolean)
    .join(" · ");
}

// A row speaks only when it has its own story; most rows are told by their
// group and stay quiet.
function ItemReasonBadge({ item, film }: { item: MissingItem; film: boolean }) {
  if (!item.reason) return null;
  // The badge is right either way; only the word is episodic. A film has a
  // release date, not a broadcast.
  const label =
    film && item.reason === "unaired"
      ? "Not released yet"
      : itemReasonLabel[item.reason];
  return (
    <span
      title={itemReasonTitle(item)}
      className={cn(
        "hidden shrink-0 items-center rounded-full border px-2.5 py-0.5 text-[11.5px] font-semibold whitespace-nowrap md:inline-flex",
        itemReasonTone[item.reason],
      )}
    >
      {item.last_pass ? `${label} · ${timeAgo(item.last_pass.at)}` : label}
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

  // An empty page can still carry a cursor: membership is decided in Go, so a
  // request that scanned its whole budget without finding a sub-cutoff release
  // returns nothing and a place to resume. Returning early here would show
  // "Nothing below cutoff" over a library that has some, further down.
  return (
    <>
      {groups.length === 0 ? (
        <EmptyState
          title={hasNextPage ? "None found yet" : "Nothing below cutoff"}
          blurb={
            hasNextPage
              ? "No episode below its cutoff in the series checked so far. Keep looking to check the rest of the library."
              : "Held episodes on a profile with upgrades enabled appear here while what holds them scores below that profile's cutoff."
          }
        />
      ) : (
        <div className="space-y-3">
          {groups.map((group) => (
            <CutoffGroupCard key={group.title_id} group={group} />
          ))}
        </div>
      )}
      <LoadMore
        hasNextPage={hasNextPage}
        isFetching={isFetchingNextPage}
        label={groups.length === 0 ? "Keep looking" : undefined}
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
      title={group.title}
      header={
        <>
          <Link
            to={`/titles/${group.title_id}`}
            className="min-w-0 flex-1 truncate text-sm font-semibold hover:underline"
          >
            {group.title}
          </Link>
          <span className="text-xs text-faint tabular-nums">
            {isFilm(group.format)
              ? "Below cutoff"
              : `${plural(group.below, "episode")} below cutoff`}
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
          titleId={group.title_id}
          cutoff={group.cutoff_score}
          item={item}
          shared={shared}
        />
      ))}
      {hidden > 0 && (
        <OverflowRow
          titleId={group.title_id}
          label={`${plural(hidden, "more episode")} not shown`}
        />
      )}
    </GroupSection>
  );
}

function CutoffRow({
  titleId,
  cutoff,
  item,
  shared,
}: {
  titleId: number;
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
            // No stated preference is missing, yet the release is still below
            // the cutoff. Deliberately two facts and no verdict: unmet goals
            // exclude the repack/v2 bonus, so an empty list means "tops every
            // preference", not "at the maximum" -- a v2 of this very release
            // scores 25 higher and would be taken.
            <div
              className="truncate text-[11px] text-faint"
              title={`This release meets every preference this profile states. It stays listed because its score (${item.score}) is below the cutoff (${cutoff}).`}
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
        <Link to={`/titles/${titleId}?item=${item.number}`}>
          <Search className="size-4" /> Search
        </Link>
      </Button>
      <RowMonitorToggle
        itemId={item.id}
        number={item.number}
        monitored={item.monitored}
      />
    </div>
  );
}

// A search is queued, never run here: titlesPerPass bounds how much of the
// indexer budget one pass can spend, so the toast says queued rather than done.
function SearchActions({
  selectedTitles,
  onDone,
}: {
  selectedTitles: number[];
  onDone: () => void;
}) {
  const queryClient = useQueryClient();
  const queue = useMutation({
    mutationFn: (titleIds: number[]) => api.queueWantedSearch(titleIds),
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
        disabled={selectedTitles.length === 0 || queue.isPending}
        onClick={() => queue.mutate(selectedTitles)}
      >
        <Search className="size-4" />
        Search selected
        {selectedTitles.length > 0 && ` (${selectedTitles.length})`}
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
  label,
  onClick,
}: {
  hasNextPage: boolean;
  isFetching: boolean;
  label?: string;
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
      {isFetching ? "Loading…" : (label ?? "Load more")}
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
