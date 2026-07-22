import { cn } from '@/lib/utils'

export function HaveProgress({ have, total }: { have: number; total: number }) {
  const pct = total > 0 ? (have / total) * 100 : 0
  const complete = total > 0 && have >= total
  return (
    <div className="flex items-center gap-2.5 sm:min-w-[140px]">
      {/* the bar needs room; on mobile we keep just the count to avoid overflow */}
      <div className="hidden h-1.5 flex-1 overflow-hidden rounded border border-border bg-panel-2 sm:block">
        <div
          className={cn('h-full', complete ? 'bg-have' : 'bg-primary')}
          style={{ width: `${pct}%` }}
        />
      </div>
      <span className="text-xs tabular-nums text-muted-foreground">
        {have} / {total}
      </span>
    </div>
  )
}
