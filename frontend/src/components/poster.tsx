import { cn } from '@/lib/utils'

/** First-letter poster placeholder (AniList covers may be absent). */
function initial(title: string): string {
  return title.trim().charAt(0).toUpperCase() || '?'
}

export function Poster({
  title,
  coverUrl,
  size = 'sm',
  className,
}: {
  title: string
  coverUrl?: string
  size?: 'sm' | 'lg'
  className?: string
}) {
  const dims =
    size === 'lg' ? 'w-[82px] h-[116px] text-3xl rounded-lg' : 'w-[34px] h-12 text-[15px] rounded-[5px]'
  if (coverUrl) {
    return (
      <img
        src={coverUrl}
        alt=""
        className={cn('flex-none border object-cover', dims, className)}
      />
    )
  }
  return (
    <div
      className={cn(
        'flex-none grid place-items-center border border-border bg-gradient-to-br from-accent to-panel-2 font-bold text-accent-foreground',
        dims,
        className,
      )}
    >
      {initial(title)}
    </div>
  )
}
