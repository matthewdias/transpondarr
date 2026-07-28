import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import {
  Check,
  ChevronLeft,
  ChevronRight,
  Clock,
  Compass,
  ExternalLink,
  Loader2,
  Plus,
  RefreshCw,
  TriangleAlert,
} from "lucide-react";
import { api, ApiError, type SeasonEntry } from "@/lib/api";
import {
  type ChartFilters,
  filterEntries,
  formatLabel,
  NO_FILTERS,
  plainDescription,
  statusLabel,
} from "@/lib/chart";
import { browseSeasonQuery, seriesQuery } from "@/lib/queries";
import { nextEpisodeLabel } from "@/lib/format";
import {
  currentSeason,
  seasonLabel,
  SEASONS,
  type SeasonName,
  type SeasonRef,
  stepSeason,
} from "@/lib/season";
import { useIsMobile } from "@/hooks/use-mobile";
import { Topbar } from "@/components/topbar";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Dialog,
  DialogContent,
  DialogDescription,
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

function entryTitle(e: SeasonEntry) {
  return e.romaji || e.english || e.native || `AniList ${e.anilist_id}`;
}

// The season picker spans anime's cataloged history; older charts are sparse
// but valid.
const YEAR_FLOOR = 1960;

export function DiscoveryPage() {
  const today = currentSeason();
  const [ref, setRef] = useState<SeasonRef>(today);
  const [filters, setFilters] = useState<ChartFilters>(NO_FILTERS);

  const chart = useQuery(browseSeasonQuery(ref));
  const entries = useMemo(() => chart.data?.entries ?? [], [chart.data]);

  // Filter options come from the unfiltered chart, so narrowing one dimension
  // never empties another's menu.
  const options = useMemo(() => {
    const formats = [...new Set(entries.map((e) => e.format))].filter(
      (f): f is string => !!f,
    );
    const statuses = [...new Set(entries.map((e) => e.status))].filter(
      (s): s is string => !!s,
    );
    const genres = [...new Set(entries.flatMap((e) => e.genres))].sort();
    return { formats, statuses, genres };
  }, [entries]);

  const filtered = useMemo(
    () => filterEntries(entries, filters),
    [entries, filters],
  );

  const isCurrent = ref.season === today.season && ref.year === today.year;
  const years = Array.from(
    { length: today.year + 1 - YEAR_FLOOR + 1 },
    (_, i) => today.year + 1 - i,
  );

  const setFilter = (key: keyof ChartFilters) => (value: string) =>
    setFilters((f) => ({ ...f, [key]: value }));

  return (
    <>
      <Topbar title="Discovery" />

      <div className="px-4 py-6 sm:px-6">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex flex-wrap items-center gap-2">
            <div className="flex items-center gap-1">
              <Button
                variant="outline"
                size="icon"
                aria-label="Previous season"
                onClick={() => setRef(stepSeason(ref, -1))}
              >
                <ChevronLeft className="size-4" />
              </Button>
              <Select
                value={ref.season}
                onValueChange={(season) =>
                  setRef({ ...ref, season: season as SeasonName })
                }
              >
                <SelectTrigger aria-label="Season" className="w-[7.5rem]">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {SEASONS.map((s) => (
                    <SelectItem key={s} value={s}>
                      {s.charAt(0).toUpperCase() + s.slice(1)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <Select
                value={String(ref.year)}
                onValueChange={(year) => setRef({ ...ref, year: Number(year) })}
              >
                <SelectTrigger aria-label="Year" className="w-[5.5rem]">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {years.map((y) => (
                    <SelectItem key={y} value={String(y)}>
                      {y}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <Button
                variant="outline"
                size="icon"
                aria-label="Next season"
                onClick={() => setRef(stepSeason(ref, 1))}
              >
                <ChevronRight className="size-4" />
              </Button>
            </div>
            {!isCurrent && (
              <Button variant="ghost" size="sm" onClick={() => setRef(today)}>
                Current season
              </Button>
            )}
          </div>

          <div className="flex flex-wrap items-center gap-2">
            <FilterSelect
              label="Format"
              value={filters.format}
              onChange={setFilter("format")}
              allLabel="All formats"
              options={options.formats.map((f) => [f, formatLabel(f)])}
            />
            <FilterSelect
              label="Status"
              value={filters.status}
              onChange={setFilter("status")}
              allLabel="All statuses"
              options={options.statuses.map((s) => [s, statusLabel(s)])}
            />
            <FilterSelect
              label="Genre"
              value={filters.genre}
              onChange={setFilter("genre")}
              allLabel="All genres"
              options={options.genres.map((g) => [g, g])}
            />
          </div>
        </div>

        <h2 className="mt-5 text-sm font-medium text-muted-foreground">
          {seasonLabel(ref)}
          {chart.isSuccess && (
            <span className="text-faint">
              {" "}
              · {filtered.length} title{filtered.length === 1 ? "" : "s"}
            </span>
          )}
        </h2>

        {chart.isError && (
          <div className="mx-auto mt-10 flex max-w-md flex-col items-center rounded-lg border border-dashed bg-card px-6 py-12 text-center">
            <TriangleAlert className="mb-3 size-6 text-dl" />
            <h3 className="text-sm font-semibold">
              Couldn’t load the season chart
            </h3>
            <p className="mt-1.5 text-sm text-muted-foreground">
              {chart.error instanceof ApiError
                ? chart.error.message
                : String(chart.error)}
            </p>
            <Button
              variant="outline"
              size="sm"
              className="mt-4"
              onClick={() => chart.refetch()}
            >
              <RefreshCw className="size-4" /> Try again
            </Button>
          </div>
        )}

        {(chart.isPending || chart.isPaused) && !chart.isError && (
          <ChartSkeleton />
        )}

        {chart.isSuccess && entries.length === 0 && (
          <div className="mx-auto mt-10 flex max-w-md flex-col items-center rounded-lg border border-dashed bg-card px-6 py-12 text-center">
            <Compass className="mb-3 size-6 text-faint" />
            <h3 className="text-sm font-semibold">Nothing charted</h3>
            <p className="mt-1.5 text-sm text-muted-foreground">
              AniList lists no titles for {seasonLabel(ref)}.
            </p>
          </div>
        )}

        {chart.isSuccess && entries.length > 0 && filtered.length === 0 && (
          <div className="mx-auto mt-10 flex max-w-md flex-col items-center rounded-lg border border-dashed bg-card px-6 py-12 text-center">
            <h3 className="text-sm font-semibold">No titles match</h3>
            <p className="mt-1.5 text-sm text-muted-foreground">
              Every title this season is filtered out.
            </p>
            <Button
              variant="outline"
              size="sm"
              className="mt-4"
              onClick={() => setFilters(NO_FILTERS)}
            >
              Clear filters
            </Button>
          </div>
        )}

        {filtered.length > 0 && (
          <div className="mt-3 grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5">
            {filtered.map((e) => (
              <SeasonCard key={e.anilist_id} entry={e} />
            ))}
          </div>
        )}
      </div>
    </>
  );
}

function FilterSelect({
  label,
  value,
  onChange,
  allLabel,
  options,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  allLabel: string;
  options: [string, string][];
}) {
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger aria-label={label} size="sm">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value="all">{allLabel}</SelectItem>
        {options.map(([v, l]) => (
          <SelectItem key={v} value={v}>
            {l}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

function SeasonCard({ entry }: { entry: SeasonEntry }) {
  const queryClient = useQueryClient();
  const [detailOpen, setDetailOpen] = useState(false);
  const title = entryTitle(entry);
  const isMovie = entry.format === "MOVIE";

  const add = useMutation({
    mutationFn: () => api.addSeries(entry.anilist_id),
    onSuccess: (series) => {
      toast.success("Series added", { description: series.title });
      invalidate();
    },
    onError: (err) => {
      if (err instanceof ApiError && err.status === 409) {
        toast.info("Already tracking", { description: title });
        invalidate();
        return;
      }
      toast.error("Could not add series", {
        description: err instanceof Error ? err.message : String(err),
      });
    },
  });

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: seriesQuery().queryKey });
    queryClient.invalidateQueries({ queryKey: ["browse-season"] });
  };

  const meta = [
    entry.format && formatLabel(entry.format),
    entry.episodes ? `${entry.episodes} ep` : null,
    entry.average_score ? `${entry.average_score}%` : null,
    entry.studio,
  ].filter(Boolean) as string[];

  const airing = nextEpisodeLabel(entry.next_episode, entry.next_airs_at);

  // One definition, rendered on both the card and the detail view.
  const actions = (
    <>
      {entry.tracked ? (
        <Button
          asChild
          variant="outline"
          size="sm"
          className="flex-1"
          title="Already in your library"
        >
          <Link to={`/series/${entry.series_id}`}>
            <Check className="size-3.5" /> In library
          </Link>
        </Button>
      ) : (
        <Button
          size="sm"
          className="flex-1"
          disabled={isMovie || add.isPending}
          title={isMovie ? "Reserved — v1 tracks series" : undefined}
          onClick={() => add.mutate()}
        >
          {add.isPending ? (
            <Loader2 className="size-3.5 animate-spin" />
          ) : (
            <Plus className="size-3.5" />
          )}
          Add
        </Button>
      )}
      <Button
        asChild
        variant="outline"
        size="sm"
        className="px-2"
        title="Open on AniList"
      >
        <a
          href={`https://anilist.co/anime/${entry.anilist_id}`}
          target="_blank"
          rel="noopener noreferrer"
          aria-label="Open on AniList"
        >
          <ExternalLink className="size-3.5" />
        </a>
      </Button>
    </>
  );

  const cover = entry.cover_url ? (
    <img
      src={entry.cover_url}
      alt=""
      loading="lazy"
      className="aspect-[2/3] w-full border-b object-cover"
    />
  ) : (
    <div className="grid aspect-[2/3] w-full place-items-center border-b bg-gradient-to-br from-accent to-panel-2 text-4xl font-bold text-accent-foreground">
      {title.trim().charAt(0).toUpperCase() || "?"}
    </div>
  );

  return (
    <div className="flex flex-col overflow-hidden rounded-lg border bg-card shadow-sm">
      <button
        type="button"
        aria-haspopup="dialog"
        onClick={() => setDetailOpen(true)}
        className="cursor-pointer text-left outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring [&:hover_[data-slot=card-title]]:underline"
      >
        {cover}
        <div
          data-slot="card-title"
          className="line-clamp-2 px-3 pt-3 text-sm font-medium leading-snug underline-offset-2"
          title={title}
        >
          {title}
        </div>
      </button>

      <div className="flex flex-1 flex-col gap-1 p-3 pt-1">
        {meta.length > 0 && (
          <div className="truncate text-xs text-muted-foreground">
            {meta.join(" · ")}
          </div>
        )}
        {entry.genres.length > 0 && (
          <div className="truncate text-xs text-faint">
            {entry.genres.join(" · ")}
          </div>
        )}
        {airing && (
          <div className="mt-0.5 flex items-center gap-1 text-xs text-muted-foreground">
            <Clock className="size-3" /> {airing}
          </div>
        )}

        <div className="mt-auto flex items-center gap-1.5 pt-2">{actions}</div>
      </div>

      <SeasonDetail
        entry={entry}
        title={title}
        meta={meta}
        airing={airing}
        actions={actions}
        open={detailOpen}
        onOpenChange={setDetailOpen}
      />
    </div>
  );
}

// The full record for one entry - everything the card compresses, in a dialog
// (drawer on mobile) so an expanded synopsis never reflows the grid.
function SeasonDetail({
  entry,
  title,
  meta,
  airing,
  actions,
  open,
  onOpenChange,
}: {
  entry: SeasonEntry;
  title: string;
  meta: string[];
  airing: string | null;
  actions: React.ReactNode;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const isMobile = useIsMobile();
  const synopsis = plainDescription(entry.description);
  const english =
    entry.english && entry.english !== title ? entry.english : null;
  const subtitle = english ?? meta.join(" · ");

  const body = (
    <>
      <div className="flex gap-4">
        {entry.cover_url ? (
          <img
            src={entry.cover_url}
            alt=""
            className="h-44 w-[118px] flex-none rounded-md border object-cover"
          />
        ) : (
          <div className="grid h-44 w-[118px] flex-none place-items-center rounded-md border bg-gradient-to-br from-accent to-panel-2 text-3xl font-bold text-accent-foreground">
            {title.trim().charAt(0).toUpperCase() || "?"}
          </div>
        )}
        <div className="min-w-0 flex-1 space-y-1.5 text-sm">
          {meta.length > 0 && (
            <div className="text-muted-foreground">{meta.join(" · ")}</div>
          )}
          {entry.status && (
            <div className="text-muted-foreground">
              {statusLabel(entry.status)}
            </div>
          )}
          {entry.genres.length > 0 && (
            <div className="text-faint">{entry.genres.join(" · ")}</div>
          )}
          {airing && (
            <div className="flex items-center gap-1 text-muted-foreground">
              <Clock className="size-3.5" /> {airing}
            </div>
          )}
          <div className="flex max-w-56 items-center gap-1.5 pt-1">
            {actions}
          </div>
        </div>
      </div>
      {synopsis ? (
        <p className="max-h-[45vh] overflow-y-auto text-sm leading-relaxed text-muted-foreground">
          {synopsis}
        </p>
      ) : (
        <p className="text-sm text-faint">No synopsis on AniList.</p>
      )}
    </>
  );

  if (isMobile) {
    return (
      <Drawer open={open} onOpenChange={onOpenChange}>
        <DrawerContent className="px-4 pb-6">
          <DrawerHeader className="px-0">
            <DrawerTitle>{title}</DrawerTitle>
            <DrawerDescription>{subtitle}</DrawerDescription>
          </DrawerHeader>
          <div className="space-y-4 overflow-y-auto">{body}</div>
        </DrawerContent>
      </Drawer>
    );
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle className="pr-6">{title}</DialogTitle>
          <DialogDescription>{subtitle}</DialogDescription>
        </DialogHeader>
        {body}
      </DialogContent>
    </Dialog>
  );
}

function ChartSkeleton() {
  return (
    <div className="mt-3 grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5">
      {Array.from({ length: 10 }).map((_, i) => (
        <div key={i} className="overflow-hidden rounded-lg border bg-card">
          <Skeleton className="aspect-[2/3] w-full rounded-none" />
          <div className="space-y-2 p-3">
            <Skeleton className="h-4 w-3/4" />
            <Skeleton className="h-3 w-1/2" />
            <Skeleton className="h-8 w-full" />
          </div>
        </div>
      ))}
    </div>
  );
}
