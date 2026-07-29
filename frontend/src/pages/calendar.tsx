import { useMemo, useState } from "react";
import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import {
  CalendarDays,
  CalendarOff,
  ChevronLeft,
  ChevronRight,
  RefreshCw,
  TriangleAlert,
} from "lucide-react";
import { ApiError, type CalendarItem, type ItemStatus } from "@/lib/api";
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
import { Topbar } from "@/components/topbar";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";

const statusDot: Record<ItemStatus, string> = {
  have: "bg-have",
  downloading: "bg-dl",
  stuck: "bg-destructive",
  deferred: "bg-dl/50",
  wanted: "bg-faint",
};

// Compact form for grid cells, where the full ItemStatusBadge cannot fit a
// 1/7-width column; the agenda list renders the real badge.
const statusLabel: Record<ItemStatus, string> = {
  have: "In library",
  downloading: "Downloading",
  stuck: "Import blocked",
  deferred: "Batch downloaded",
  wanted: "Wanted",
};

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

      <div className="px-4 py-6 sm:px-6">
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
            </div>
            <h2 className="min-w-40 text-sm font-medium">{label}</h2>
            <Button
              variant="ghost"
              size="sm"
              onClick={() => setAnchor(new Date())}
            >
              Today
            </Button>
          </div>

          <div className="flex flex-wrap items-center gap-3">
            <label className="flex items-center gap-2 text-xs text-muted-foreground">
              <Switch
                checked={unmonitored}
                onCheckedChange={setUnmonitored}
                aria-label="Show unmonitored series"
              />
              Unmonitored
            </label>
            <Tabs
              value={view}
              onValueChange={(v) => setView(v as CalendarView)}
            >
              <TabsList>
                <TabsTrigger value="month">Month</TabsTrigger>
                <TabsTrigger value="week">Week</TabsTrigger>
                <TabsTrigger value="agenda">Agenda</TabsTrigger>
              </TabsList>
            </Tabs>
          </div>
        </div>

        {cal.isError && (
          <div className="mx-auto mt-10 flex max-w-md flex-col items-center rounded-lg border border-dashed bg-card px-6 py-12 text-center">
            <TriangleAlert className="mb-3 size-6 text-dl" />
            <h3 className="text-sm font-semibold">
              Couldn’t load the calendar
            </h3>
            <p className="mt-1.5 text-sm text-muted-foreground">
              {cal.error instanceof ApiError
                ? cal.error.message
                : String(cal.error)}
            </p>
            <Button
              variant="outline"
              size="sm"
              className="mt-4"
              onClick={() => cal.refetch()}
            >
              <RefreshCw className="size-4" /> Try again
            </Button>
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

            {cal.data.unscheduled.length > 0 && (
              <div className="mt-6 flex flex-wrap items-baseline gap-x-1.5 gap-y-1 rounded-lg border border-dashed bg-card px-4 py-3 text-xs text-muted-foreground">
                <CalendarOff className="size-3.5 self-center text-faint" />
                <span className="font-medium">No schedule data:</span>
                {cal.data.unscheduled.map((s, i) => (
                  <span key={s.series_id}>
                    <Link
                      to={`/series/${s.series_id}`}
                      className="underline-offset-2 hover:underline"
                    >
                      {s.title}
                    </Link>
                    {i < cal.data.unscheduled.length - 1 && ","}
                  </span>
                ))}
                <span className="text-faint">
                  — AniList publishes no air dates for these, so their episodes
                  can’t be placed on the calendar.
                </span>
              </div>
            )}
          </div>
        )}
      </div>
    </>
  );
}

function EntryLine({ item }: { item: CalendarItem }) {
  return (
    <Link
      to={`/series/${item.series_id}`}
      className="block truncate rounded px-1 py-0.5 text-xs leading-5 hover:bg-panel-2"
      title={`${item.series_title} — episode ${item.number} (${item.status})`}
    >
      <span
        className={cn(
          "mr-1.5 inline-block size-1.5 rounded-full align-middle",
          statusDot[item.status],
        )}
      />
      <span className="tabular-nums text-faint">{pad2(item.number)}</span>{" "}
      {item.series_title}
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
                  to={`/series/${item.series_id}`}
                  className="block overflow-hidden rounded-md border bg-panel-2/40 p-2 hover:bg-panel-2"
                >
                  <div className="truncate text-xs font-medium">
                    {item.series_title}
                  </div>
                  <div className="mt-0.5 text-xs text-muted-foreground">
                    Ep {item.number} · {timeLabel(item.airs_at)}
                  </div>
                  <div
                    className="mt-1.5 flex items-center gap-1.5 text-xs text-muted-foreground"
                    title={item.import_error || undefined}
                  >
                    <span
                      className={cn(
                        "size-1.5 flex-none rounded-full",
                        statusDot[item.status],
                      )}
                    />
                    <span className="truncate">{statusLabel[item.status]}</span>
                  </div>
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
      <div className="mx-auto flex max-w-md flex-col items-center rounded-lg border border-dashed bg-card px-6 py-12 text-center">
        <CalendarDays className="mb-3 size-6 text-faint" />
        <h3 className="text-sm font-semibold">Nothing airing</h3>
        <p className="mt-1.5 text-sm text-muted-foreground">
          No monitored episodes are scheduled this week.
        </p>
      </div>
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
                  to={`/series/${item.series_id}`}
                  className="flex items-center gap-3 px-3 py-2.5 hover:bg-panel-2/50"
                >
                  <span className="w-16 whitespace-nowrap text-xs tabular-nums text-muted-foreground">
                    {timeLabel(item.airs_at)}
                  </span>
                  <span className="min-w-0 flex-1 truncate text-sm">
                    {item.series_title}
                    <span className="ml-1.5 text-xs text-faint">
                      Ep {item.number}
                    </span>
                  </span>
                  <span className="hidden text-xs text-faint sm:block">
                    {airDate(item.airs_at)}
                  </span>
                  <ItemStatusBadge
                    status={item.status}
                    error={item.import_error}
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
