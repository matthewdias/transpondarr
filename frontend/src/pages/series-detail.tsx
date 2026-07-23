import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { api, ApiError, type SeriesDetail } from '@/lib/api'
import { cn } from '@/lib/utils'
import { Topbar } from '@/components/topbar'
import { Poster } from '@/components/poster'
import { Switch } from '@/components/ui/switch'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { EpisodesTab } from '@/components/detail/episodes-tab'
import { ReleasesTab } from '@/components/detail/releases-tab'
import { HistoryTab } from '@/components/detail/history-tab'

type TabKey = 'episodes' | 'releases' | 'history'

export function SeriesDetailPage() {
  const params = useParams()
  const id = Number(params.id)
  const [tab, setTab] = useState<TabKey>('episodes')
  const queryClient = useQueryClient()

  const { data: detail, isLoading, isError, error } = useQuery({
    queryKey: ['series', id],
    queryFn: ({ signal }) => api.getSeries(id, signal),
    enabled: Number.isFinite(id),
  })

  const monitor = useMutation({
    mutationFn: (v: boolean) => api.setMonitored(id, v),
    onMutate: async (v) => {
      await queryClient.cancelQueries({ queryKey: ['series', id] })
      const prev = queryClient.getQueryData<SeriesDetail>(['series', id])
      queryClient.setQueryData<SeriesDetail>(['series', id], (old) =>
        old ? { ...old, monitored: v } : old,
      )
      return { prev }
    },
    onError: (_e, _v, ctx) => {
      if (ctx?.prev) queryClient.setQueryData(['series', id], ctx.prev)
      toast.error('Could not update monitoring')
    },
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: ['series', id] })
      queryClient.invalidateQueries({ queryKey: ['series'] })
    },
  })

  const notFound = isError && error instanceof ApiError && error.status === 404

  const breadcrumb = (
    <div className="flex min-w-0 items-center gap-2">
      <Link to="/" className="font-medium text-faint hover:text-foreground">
        Series
      </Link>
      <span className="text-faint">/</span>
      <h1 className="truncate text-base font-semibold tracking-tight">
        {detail?.title ?? (notFound ? 'Not found' : '…')}
      </h1>
    </div>
  )

  const goSearch = () => setTab('releases')

  return (
    <>
      <Topbar breadcrumb={breadcrumb} />
      <div className="px-4 py-6 sm:px-6">
        {isLoading && <HeaderSkeleton />}

        {notFound && (
          <div className="rounded-lg border border-dashed bg-card px-6 py-16 text-center">
            <h2 className="text-base font-semibold">Series not found</h2>
            <p className="mt-2 text-sm text-muted-foreground">
              It may have been removed.{' '}
              <Link to="/" className="text-accent-foreground hover:underline">
                Back to series
              </Link>
              .
            </p>
          </div>
        )}

        {isError && !notFound && (
          <div className="rounded-lg border border-destructive/40 bg-destructive/5 px-4 py-3 text-sm text-destructive">
            Failed to load series: {error instanceof Error ? error.message : String(error)}
          </div>
        )}

        {detail && (
          <>
            <DetailHeader
              detail={detail}
              onToggleMonitored={(v) => monitor.mutate(v)}
            />

            <Tabs
              value={tab}
              onValueChange={(v) => setTab(v as TabKey)}
              className="mt-1 gap-0"
            >
              <TabsList
                variant="line"
                className="mb-[18px] h-auto w-full justify-start gap-0.5 rounded-none border-b bg-transparent p-0"
              >
                <DetailTab value="episodes" label="Episodes" count={detail.items.length} active={tab === 'episodes'} />
                <DetailTab value="releases" label="Releases" active={tab === 'releases'} />
                <DetailTab value="history" label="History" active={tab === 'history'} />
              </TabsList>

              <TabsContent value="episodes">
                <EpisodesTab detail={detail} onSearchAll={goSearch} />
              </TabsContent>
              <TabsContent value="releases">
                <ReleasesTab seriesId={id} active={tab === 'releases'} />
              </TabsContent>
              <TabsContent value="history">
                <HistoryTab seriesId={id} active={tab === 'history'} />
              </TabsContent>
            </Tabs>
          </>
        )}
      </div>
    </>
  )
}

function DetailHeader({
  detail,
  onToggleMonitored,
}: {
  detail: SeriesDetail
  onToggleMonitored: (v: boolean) => void
}) {
  const subtitle = [detail.english, detail.native].filter(Boolean).join(' · ')
  const chips = [
    detail.format,
    `${detail.items.length} episodes`,
    statusLabel(detail.status) || null,
  ].filter(Boolean) as string[]

  return (
    <div className="mb-5 flex items-start gap-4 sm:gap-5">
      <Poster title={detail.title} size="lg" className="hidden sm:grid" />
      <div className="min-w-0 flex-1">
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0">
            <h1 className="text-xl font-semibold tracking-tight sm:text-2xl">
              {detail.title}
            </h1>
            {subtitle && <p className="mt-0.5 text-sm text-faint">{subtitle}</p>}
          </div>
          <label className="flex flex-none cursor-pointer items-center gap-2.5 text-[13.5px] font-medium">
            <span className={detail.monitored ? 'text-foreground' : 'text-muted-foreground'}>
              {detail.monitored ? 'Monitored' : 'Unmonitored'}
            </span>
            <Switch
              checked={detail.monitored}
              onCheckedChange={onToggleMonitored}
              aria-label="Toggle monitoring"
            />
          </label>
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
          {detail.anilist_id ? (
            <a
              href={`https://anilist.co/anime/${detail.anilist_id}`}
              target="_blank"
              rel="noreferrer"
              className="inline-flex items-center rounded-md border border-border bg-panel-2 px-2.5 py-1 font-mono text-[11.5px] font-medium text-muted-foreground hover:text-accent-foreground"
            >
              AniList {detail.anilist_id}
            </a>
          ) : null}
        </div>
      </div>
    </div>
  )
}

function DetailTab({
  value,
  label,
  count,
  active,
}: {
  value: TabKey
  label: string
  count?: number
  active: boolean
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
            'rounded-full border px-1.5 text-[11px] tabular-nums',
            active
              ? 'border-transparent bg-accent text-accent-foreground'
              : 'border-border bg-background text-faint',
          )}
        >
          {count}
        </span>
      )}
    </TabsTrigger>
  )
}

function statusLabel(status?: string): string {
  if (!status) return ''
  // AniList statuses are SCREAMING_CASE — title-case them.
  return status.charAt(0) + status.slice(1).toLowerCase()
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
  )
}
