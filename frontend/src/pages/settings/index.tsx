import { useQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { Topbar } from '@/components/topbar'
import { Skeleton } from '@/components/ui/skeleton'
import { DownloadSection } from './sections/download'
import { IndexerSection } from './sections/indexer'
import { LibrarySection } from './sections/library'
import { AuthSection } from './sections/auth'
import { ApiKeySection } from './sections/api-key'
import { GeneralSection } from './sections/general'

export function SettingsPage() {
  const { data, isLoading, isError, error } = useQuery({
    queryKey: ['settings'],
    queryFn: ({ signal }) => api.getSettings(signal),
  })

  return (
    <>
      <Topbar title="Settings" />
      <div className="mx-auto max-w-2xl px-4 py-6 sm:px-6">
        {isError && (
          <div className="rounded-lg border border-destructive/40 bg-destructive/5 px-4 py-3 text-sm text-destructive">
            Failed to load settings: {error instanceof Error ? error.message : String(error)}
          </div>
        )}
        {isLoading && <SettingsSkeleton />}
        {data && (
          <div className="space-y-5">
            <DownloadSection settings={data} />
            <IndexerSection settings={data} />
            <LibrarySection settings={data} />
            <AuthSection settings={data} />
            <ApiKeySection settings={data} />
            <GeneralSection settings={data} />
          </div>
        )}
      </div>
    </>
  )
}

function SettingsSkeleton() {
  return (
    <div className="space-y-5">
      {Array.from({ length: 3 }).map((_, i) => (
        <div key={i} className="rounded-lg border bg-card p-4 shadow-sm">
          <Skeleton className="mb-4 h-5 w-40" />
          <div className="space-y-3">
            <Skeleton className="h-9 w-full" />
            <Skeleton className="h-9 w-full" />
          </div>
        </div>
      ))}
    </div>
  )
}
