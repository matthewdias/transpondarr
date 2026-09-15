import { useMemo, useState } from "react";
import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import {
  CalendarDays,
  CalendarClock,
  CalendarOff,
  Check,
  Download,
  EyeOff,
  ChevronLeft,
  ChevronRight,
  Film,
  FolderClock,
  TriangleAlert,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import {
  type CalendarItem,
  type ItemStatus,
  type UnscheduledTitle,
} from "@/lib/api";
import {
  bucketByDay,
  type CalendarView,
  dayKey,
  fetchRange,
  stepAnchor,
  timeLabel,
  visibleDays,
} from "@/lib/calendar";
import { calendarQuery } from "@/lib/queries";
import { airDate, pad2 } from "@/lib/format";
import { cn } from "@/lib/utils";
import { MOBILE_BREAKPOINT } from "@/hooks/use-mobile";
import { ItemStatusBadge } from "@/components/badges";
import { EmptyState } from "@/components/empty-state";
import { LoadError } from "@/components/load-error";
import { Topbar } from "@/components/topbar";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Toggle } from "@/components/ui/toggle";

const statusDot: Record<ItemStatus, string> = {
  in_library: "bg-have",
  downloading: "bg-dl",
  stuck: "bg-destructive",
  deferred: "bg-dl/50",
  wanted: "bg-faint",
};

// The badges' glyphs, so a month entry's status doesn't depend on colour alone;
// wanted keeps the plain dot, which is a shape of its own.
const statusGlyph: Partial<Record<ItemStatus, LucideIcon>> = {
  in_library: Check,
  downloading: Download,
  stuck: TriangleAlert,
  deferred: FolderClock,
};

const statusTone: Record<ItemStatus, string> = {
  in_library: "text-have",
  downloading: "text-dl",
  stuck: "text-destructive",
  deferred: "text-dl",
  wanted: "text-faint",
};

// Compact form for grid cells, where the full ItemStatusBadge cannot fit a
// 1/7-width column; the agenda list renders the real badge.
const statusLabel: Record<ItemStatus, string> = {
  in_library: "In library",
  downloading: "Downloading",
  stuck: "Import blocked",
  deferred: "Batch downloaded",
  wanted: "Wanted",
};

// Format alone (#208), so a one-item OVA keeps its episode line. A premiere
// states no time anywhere below: a film's airs_at is either a real TV-premiere
// instant or a date-only release stored at noon UTC to name a day, and nothing
// here can distinguish them, so any clock or countdown might be invented.
const isPremiere = (item: CalendarItem) => item.format === "MOVIE";

// A film's deferral is a size tie or an unextracted archive (#210), never a
// batch. Worded exactly as ItemStatusBadge does, so the two renderers on this
// page show the same wording; every other status reads the same either way.
const statusText = (item: CalendarItem) =>
  isPremiere(item) && item.status === "deferred"
    ? "Downloaded, not imported"
    : statusLabel[item.status];

export function CalendarPage() {
  // Read the viewport synchronously: deriving this from useIsMobile would
  // render (and fetch) the month view once before the effect flips to agenda.
  const [view, setView] = useState<CalendarView>(() =>
    window.innerWidth < MOBILE_BREAKPOINT ? "agenda" : "month",
  );
  const [anchor, setAnchor] = useState(() => new Date());
  const [unmonitored, setUnmonitored] = useState(false);

  const days = useMemo(() => visibleDays(view, anchor), [view, anchor]);
  const range = useMemo(() => fetchRange(days), [days]);
  const cal = useQuery(calendarQuery(range.start, range.end, unmonitored));

  const buckets = useMemo(() => bucketByDay(cal.data?.items ?? []), [cal.data]);
  const todayKey = dayKey(new Date());

  const label =
    view === "month"
      ? anchor.toLocaleDateString(undefined, {
          month: "long",
          year: "numeric",
        })
      : `${days[0].toLocaleDateString(undefined, { month: "short", day: "numeric" })} – ${days[6].toLocaleDateString(undefined, { month: "short", day: "numeric", year: "numeric" })}`;

  return (
    <>
      <Topbar title="Calendar" />

      <Tabs
        value={view}
        onValueChange={(v) => setView(v as CalendarView)}
        className="block px-4 py-6 sm:px-6"
      >
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex flex-wrap items-center gap-2">
            <div className="flex items-center gap-1">
              <Button
                variant="outline"
                size="icon"
                aria-label="Previous"
                onClick={() => setAnchor(stepAnchor(view, anchor, -1))}
              >
                <ChevronLeft className="size-4" />
              </Button>
              <Button
                variant="outline"
                size="icon"
                aria-label="Next"
                onClick={() => setAnchor(stepAnchor(view, anchor, 1))}
              >
                <ChevronRight className="size-4" />
              </Button>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => setAnchor(new Date())}
              >
                Today
              </Button>
            </div>
            <h2 className="text-sm font-medium">{label}</h2>
          </div>

          <div className="flex flex-wrap items-center gap-3">
            <span className="text-xs text-muted-foreground">Include</span>
            <Toggle
              variant="chip"
              size="chip"
              pressed={unmonitored}
              onPressedChange={setUnmonitored}
              aria-label="Show unmonitored titles"
            >
              <EyeOff /> Unmonitored
            </Toggle>
            <TabsList>
              <TabsTrigger value="month">Month</TabsTrigger>
              <TabsTrigger value="week">Week</TabsTrigger>
              <TabsTrigger value="agenda">Agenda</TabsTrigger>
            </TabsList>
          </div>
        </div>

        <TabsContent
          value={view}
          className="rounded-lg focus-visible:ring-[3px] focus-visible:ring-ring/75 dark:focus-visible:ring-ring/50"
        >
          {cal.isError && (
            <div className="mt-4">
              <LoadError
                what="the calendar"
                error={cal.error}
                onRetry={() => cal.refetch()}
              />
            </div>
          )}

          {(cal.isPending || cal.isPaused) && !cal.isError && (
            <CalendarSkeleton />
          )}

          {cal.isSuccess && (
            <div className={cn("mt-4", cal.isPlaceholderData && "opacity-50")}>
              {view === "month" && (
                <MonthGrid
                  days={days}
                  anchor={anchor}
                  buckets={buckets}
                  todayKey={todayKey}
                />
              )}
              {view === "week" && (
                <WeekGrid days={days} buckets={buckets} todayKey={todayKey} />
              )}
              {view === "agenda" && (
                <Agenda days={days} buckets={buckets} todayKey={todayKey} />
              )}

              <UnscheduledNote
                icon={CalendarOff}
                label="No schedule data:"
                titles={cal.data.unscheduled.filter((s) => s.schedule_checked)}
                explanation="AniList publishes no air dates for these, so nothing they are still missing can be placed on the calendar."
              />
              <UnscheduledNote
                icon={CalendarClock}
                label="Not checked yet:"
                titles={cal.data.unscheduled.filter((s) => !s.schedule_checked)}
                explanation="Their broadcast times have not been looked up yet, and they will be placed once they are."
              />
            </div>
          )}
        </TabsContent>
      </Tabs>
    </>
  );
}

// The calendar cannot place these, and the two reasons are not interchangeable:
// one is the provider's answer, the other is that we have not asked yet (#183).
function UnscheduledNote({
  icon: Icon,
  label,
  titles,
  explanation,
}: {
  icon: LucideIcon;
  label: string;
  titles: UnscheduledTitle[];
  explanation: string;
}) {
  if (titles.length === 0) return null;
  return (
    <div className="mt-6 flex flex-wrap items-baseline gap-x-1.5 gap-y-1 rounded-lg border border-dashed bg-card px-4 py-3 text-xs text-muted-foreground">
      <Icon className="size-3.5 self-center text-faint" />
      <span className="font-medium">{label}</span>
      {titles.map((s, i) => (
        <span key={s.title_id}>
          <Link
            to={`/titles/${s.title_id}`}
            className="underline-offset-2 hover:underline"
          >
            {s.title}
          </Link>
          {i < titles.length - 1 && ","}
        </span>
      ))}
      <span className="text-faint">— {explanation}</span>
    </div>
  );
}

function EntryLine({ item }: { item: CalendarItem }) {
  const premiere = isPremiere(item);
  const Glyph = statusGlyph[item.status];
  return (
    <Link
      to={`/titles/${item.title_id}`}
      className="block truncate rounded px-1 py-0.5 text-xs leading-5 hover:bg-panel-2"
      title={`${item.title} — ${premiere ? "premiere" : `episode ${item.number}`} (${statusText(item)})`}
    >
      {Glyph ? (
        <Glyph
          data-status-marker
          aria-hidden
          className={cn(
            "mr-1 inline-block size-3 align-middle",
            statusTone[item.status],
          )}
        />
      ) : (
        <span
          data-status-marker
          aria-hidden
          className={cn(
            "mr-1.5 ml-[3px] inline-block size-1.5 rounded-full align-middle",
            statusDot[item.status],
          )}
        />
      )}
      {/* A month cell is a seventh of the grid, so the marker is an icon where
          an episode gets its number; the title and sr-only text contain the words. */}
      {premiere ? (
        <Film
          aria-hidden
          className="mr-1 inline-block size-3 align-middle text-faint"
        />
      ) : (
        <span className="tabular-nums text-faint">{pad2(item.number)} </span>
      )}
      {item.title}
      <span className="sr-only">, {statusText(item)}</span>
    </Link>
  );
}

function MonthGrid({
  days,
  anchor,
  buckets,
  todayKey,
}: {
  days: Date[];
  anchor: Date;
  buckets: Map<string, CalendarItem[]>;
  todayKey: string;
}) {
  return (
    <div className="overflow-hidden rounded-lg border bg-card">
      <div className="grid grid-cols-7 border-b bg-panel-2/50">
        {days.slice(0, 7).map((d) => (
          <div
            key={dayKey(d)}
            className="px-2 py-1.5 text-center text-xs font-medium text-muted-foreground"
          >
            {d.toLocaleDateString(undefined, { weekday: "short" })}
          </div>
        ))}
      </div>
      <div className="grid grid-cols-7">
        {days.map((d, i) => {
          const key = dayKey(d);
          const inMonth = d.getMonth() === anchor.getMonth();
          return (
            <div
              key={key}
              className={cn(
                "min-h-24 border-b p-1",
                i % 7 !== 0 && "border-l",
                i >= days.length - 7 && "border-b-0",
                !inMonth && "bg-panel-2/30",
              )}
            >
              <div
                className={cn(
                  "mb-0.5 grid size-6 place-items-center rounded-full text-xs tabular-nums",
                  key === todayKey
                    ? "bg-primary font-semibold text-primary-foreground"
                    : inMonth
                      ? "text-muted-foreground"
                      : "text-faint",
                )}
              >
                {d.getDate()}
              </div>
              {(buckets.get(key) ?? []).map((item) => (
                <EntryLine key={item.id} item={item} />
              ))}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function WeekGrid({
  days,
  buckets,
  todayKey,
}: {
  days: Date[];
  buckets: Map<string, CalendarItem[]>;
  todayKey: string;
}) {
  return (
    <div className="grid grid-cols-1 gap-px overflow-hidden rounded-lg border bg-border sm:grid-cols-7">
      {days.map((d) => {
        const key = dayKey(d);
        const items = buckets.get(key) ?? [];
        return (
          <div key={key} className="min-h-32 bg-card p-2">
            <div
              className={cn(
                "mb-2 text-xs font-medium",
                key === todayKey ? "text-primary" : "text-muted-foreground",
              )}
            >
              {d.toLocaleDateString(undefined, {
                weekday: "short",
                day: "numeric",
              })}
            </div>
            <div className="space-y-2">
              {items.map((item) => (
                <Link
                  key={item.id}
                  to={`/titles/${item.title_id}`}
                  className="block overflow-hidden rounded-md border bg-panel-2/40 p-2 hover:bg-panel-2"
                >
                  <div className="truncate text-xs font-medium">
                    {item.title}
                  </div>
                  <div className="mt-0.5 text-xs text-muted-foreground">
                    {isPremiere(item)
                      ? "Premiere"
                      : `Ep ${item.number} · ${timeLabel(item.airs_at)}`}
                  </div>
                  <div className="mt-1.5 flex items-center gap-1.5 text-xs text-muted-foreground">
                    <span
                      className={cn(
                        "size-1.5 flex-none rounded-full",
                        statusDot[item.status],
                      )}
                    />
                    <span className="truncate">{statusText(item)}</span>
                  </div>
                  <ImportError error={item.import_error} className="mt-1" />
                </Link>
              ))}
            </div>
          </div>
        );
      })}
    </div>
  );
}

function Agenda({
  days,
  buckets,
  todayKey,
}: {
  days: Date[];
  buckets: Map<string, CalendarItem[]>;
  todayKey: string;
}) {
  const withItems = days.filter((d) => buckets.has(dayKey(d)));
  if (withItems.length === 0) {
    return (
      <EmptyState
        icon={CalendarDays}
        title="Nothing scheduled"
        blurb="Nothing monitored is scheduled this week."
      />
    );
  }
  return (
    <div className="space-y-4">
      {withItems.map((d) => {
        const key = dayKey(d);
        return (
          <div key={key}>
            <h3
              className={cn(
                "mb-1.5 text-xs font-semibold uppercase tracking-wide",
                key === todayKey ? "text-primary" : "text-muted-foreground",
              )}
            >
              {d.toLocaleDateString(undefined, {
                weekday: "long",
                month: "short",
                day: "numeric",
              })}
              {key === todayKey && " · Today"}
            </h3>
            <div className="divide-y overflow-hidden rounded-lg border bg-card">
              {(buckets.get(key) ?? []).map((item) => (
                <Link
                  key={item.id}
                  to={`/titles/${item.title_id}`}
                  className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2.5 hover:bg-panel-2/50"
                >
                  <span className="w-16 whitespace-nowrap text-xs tabular-nums text-muted-foreground">
                    {isPremiere(item) ? "" : timeLabel(item.airs_at)}
                  </span>
                  <span className="min-w-0 flex-1 truncate text-sm">
                    {item.title}
                    <span className="ml-1.5 text-xs text-faint">
                      {isPremiere(item) ? "Premiere" : `Ep ${item.number}`}
                    </span>
                  </span>
                  {/* airDate counts down in hours within a week, which is the
                      precision a premiere may not have. */}
                  <span className="hidden text-xs text-faint sm:block">
                    {isPremiere(item) ? "" : airDate(item.airs_at)}
                  </span>
                  <ItemStatusBadge
                    status={item.status}
                    error={item.import_error}
                    movie={isPremiere(item)}
                    plain
                  />
                  <ImportError
                    error={item.import_error}
                    className="basis-full pl-19"
                  />
                </Link>
              ))}
            </div>
          </div>
        );
      })}
    </div>
  );
}

// A button inside a link is invalid, so a link-wrapped row shows the error as
// text instead of behind a toggletip.
function ImportError({
  error,
  className,
}: {
  error?: string;
  className?: string;
}) {
  if (!error) return null;
  return (
    <div className={cn("text-xs break-words text-destructive", className)}>
      {error}
    </div>
  );
}

function CalendarSkeleton() {
  return (
    <div className="mt-4 overflow-hidden rounded-lg border bg-card">
      <div className="grid grid-cols-7 gap-px bg-border">
        {Array.from({ length: 35 }).map((_, i) => (
          <div key={i} className="min-h-24 space-y-2 bg-card p-2">
            <Skeleton className="size-6 rounded-full" />
            {i % 3 === 0 && <Skeleton className="h-3 w-full" />}
          </div>
        ))}
      </div>
    </div>
  );
}
