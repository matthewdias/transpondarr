import { Check, Download, FolderClock } from 'lucide-react'
import { cn } from '@/lib/utils'
import type { ItemStatus } from '@/lib/api'

const badgeBase =
  'inline-flex items-center gap-1 rounded-full border px-2.5 py-0.5 text-[11.5px] font-semibold whitespace-nowrap'

export function FormatBadge({ format }: { format: string }) {
  return (
    <span className={cn(badgeBase, 'border-border bg-panel-2 text-muted-foreground')}>
      {format}
    </span>
  )
}

export function MonitoredBadge({ monitored }: { monitored: boolean }) {
  return monitored ? (
    <span className={cn(badgeBase, 'border-transparent bg-accent text-accent-foreground')}>
      Monitored
    </span>
  ) : (
    <span className={cn(badgeBase, 'border-border bg-panel-2 text-faint')}>
      Unmonitored
    </span>
  )
}

export function ItemStatusBadge({ status }: { status: ItemStatus }) {
  switch (status) {
    case 'have':
      return (
        <span className={cn(badgeBase, 'border-transparent bg-have-weak text-have')}>
          <Check className="size-3" /> In library
        </span>
      )
    case 'downloading':
      return (
        <span className={cn(badgeBase, 'border-transparent bg-dl-weak text-dl')}>
          <Download className="size-3" /> Downloading
        </span>
      )
    case 'deferred':
      return (
        <span
          className={cn(badgeBase, 'border-dl/40 bg-transparent text-dl')}
          title="A batch was downloaded but no single episode file could be imported. Grab a single-episode release to replace it."
        >
          <FolderClock className="size-3" /> Batch downloaded
        </span>
      )
    default:
      return (
        <span className={cn(badgeBase, 'border-border bg-transparent text-faint')}>
          Wanted
        </span>
      )
  }
}
