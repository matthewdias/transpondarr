import { useQuery } from '@tanstack/react-query'
import { Check, Download, FolderClock, History, RefreshCw, TriangleAlert } from 'lucide-react'
import { api, ApiError, type GrabEvent } from '@/lib/api'
import { timeAgo } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Item, ItemContent, ItemMedia, ItemActions, ItemGroup } from '@/components/ui/item'
import { Skeleton } from '@/components/ui/skeleton'

function present(status: string) {
  switch (status) {
    case 'imported':
      return { verb: 'Imported', icon: Check, tone: 'bg-have-weak text-have' }
    case 'import_deferred':
      return { verb: 'Downloaded (batch)', icon: FolderClock, tone: 'bg-panel-2 text-muted-foreground' }
    default:
      return { verb: 'Downloading', icon: Download, tone: 'bg-dl-weak text-dl' }
  }
}

export function HistoryTab({ seriesId, active }: { seriesId: number; active: boolean }) {
  const { data: events, isLoading, isPaused, isError, error, refetch } = useQuery({
    queryKey: ['grabs', seriesId],
    queryFn: ({ signal }) => api.listGrabs(seriesId, signal),
    enabled: active,
  })

  // A paused retry (browser offline) reports neither fetching nor error.
  if (isLoading || isPaused) {
    return (
      <div className="overflow-hidden rounded-lg border bg-card shadow-sm">
        {Array.from({ length: 2 }).map((_, i) => (
          <div key={i} className="flex items-center gap-3 border-b px-3.5 py-3 last:border-b-0">
            <Skeleton className="size-8 rounded-lg" />
            <div className="flex-1 space-y-1.5">
              <Skeleton className="h-3.5 w-40" />
              <Skeleton className="h-3 w-56" />
            </div>
          </div>
        ))}
      </div>
    )
  }

  if (isError) {
    return (
      <div className="flex flex-col items-center rounded-lg border border-dashed bg-card px-6 py-14 text-center">
        <TriangleAlert className="mb-3 size-7 text-dl" />
        <h3 className="text-sm font-semibold">Couldn’t load history</h3>
        <p className="mt-1.5 max-w-md text-sm text-muted-foreground">
          {error instanceof ApiError ? error.message : String(error)}
        </p>
        <Button variant="outline" size="sm" className="mt-4" onClick={() => refetch()}>
          <RefreshCw className="size-4" /> Try again
        </Button>
      </div>
    )
  }

  if (!events || events.length === 0) {
    return (
      <div className="flex flex-col items-center rounded-lg border border-dashed bg-card py-16 text-center">
        <History className="mb-3 size-7 text-faint" />
        <p className="text-sm text-muted-foreground">
          No grab or import history yet. Grab a release from the Releases tab.
        </p>
      </div>
    )
  }

  return (
    <ItemGroup className="overflow-hidden rounded-lg border bg-card shadow-sm [&>*+*]:border-t">
      {events.map((e) => (
        <HistoryRow key={e.id} event={e} />
      ))}
    </ItemGroup>
  )
}

function HistoryRow({ event }: { event: GrabEvent }) {
  const { verb, icon: Icon, tone } = present(event.status)
  return (
    <Item className="gap-3">
      <ItemMedia>
        <span className={cn('grid size-8 place-items-center rounded-lg', tone)}>
          <Icon className="size-4" />
        </span>
      </ItemMedia>
      <ItemContent className="min-w-0 gap-0.5">
        <div className="text-sm font-medium">
          {verb} · Episode {event.item_number}
        </div>
        <div className="line-clamp-1 font-mono text-[12px] text-faint">
          {event.release_title}
        </div>
      </ItemContent>
      <ItemActions>
        <span className="text-xs text-faint">{timeAgo(event.created_at)}</span>
      </ItemActions>
    </Item>
  )
}
