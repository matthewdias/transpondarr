import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { Plus, Search, Loader2 } from 'lucide-react'
import { api, ApiError, type Candidate } from '@/lib/api'
import { useDebounce } from '@/hooks/use-debounce'
import { useIsMobile } from '@/hooks/use-mobile'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Poster } from '@/components/poster'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from '@/components/ui/dialog'
import {
  Drawer,
  DrawerContent,
  DrawerHeader,
  DrawerTitle,
  DrawerDescription,
} from '@/components/ui/drawer'
import { Item, ItemContent, ItemMedia, ItemActions } from '@/components/ui/item'

function candidateTitle(c: Candidate) {
  return c.romaji || c.english || c.native || `AniList ${c.anilist_id}`
}

function AddSeriesBody({ onDone }: { onDone: () => void }) {
  const [term, setTerm] = useState('')
  const debounced = useDebounce(term, 350)
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const search = useQuery({
    queryKey: ['metadata-search', debounced],
    queryFn: () => api.searchMetadata(debounced),
    enabled: debounced.trim().length > 0,
  })

  const add = useMutation({
    mutationFn: (c: Candidate) => api.addSeries(c.anilist_id),
    onSuccess: (series) => {
      queryClient.invalidateQueries({ queryKey: ['series'] })
      toast.success('Series added', {
        description: `${series.title} — ${series.items.length} wanted items expanded`,
      })
      onDone()
      navigate(`/series/${series.id}`)
    },
    onError: (err, c) => {
      if (err instanceof ApiError && err.status === 409) {
        toast.info('Already tracking', { description: candidateTitle(c) })
        onDone()
        return
      }
      toast.error('Could not add series', {
        description: err instanceof Error ? err.message : String(err),
      })
    },
  })

  const results = search.data ?? []

  return (
    <div>
      <div className="relative">
        <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-faint" />
        <Input
          autoFocus
          value={term}
          onChange={(e) => setTerm(e.target.value)}
          placeholder="Search AniList…"
          className="pl-9"
        />
      </div>

      <div className="mt-2 max-h-[56vh] min-h-[8rem] overflow-y-auto">
        {search.isFetching && (
          <div className="flex items-center justify-center gap-2 py-10 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" /> Searching…
          </div>
        )}

        {!search.isFetching && debounced.trim() && results.length === 0 && (
          <p className="py-10 text-center text-sm text-muted-foreground">
            No titles found for “{debounced}”.
          </p>
        )}

        {!debounced.trim() && (
          <p className="py-10 text-center text-sm text-faint">
            Type a title to search AniList.
          </p>
        )}

        {results.map((c) => {
          const isMovie = c.format === 'MOVIE'
          const meta = [
            c.format && `${c.format}${c.episodes ? ` · ${c.episodes} ep` : ''}`,
            [c.year, c.status].filter(Boolean).join(' · ') || null,
          ].filter(Boolean) as string[]
          const english = c.english && c.english !== candidateTitle(c) ? c.english : null
          return (
            <Item key={c.anilist_id} className="gap-3">
              <ItemMedia>
                <Poster title={candidateTitle(c)} coverUrl={c.cover_url} />
              </ItemMedia>
              <ItemContent className="min-w-0 gap-0.5">
                <div className="truncate text-sm font-medium">{candidateTitle(c)}</div>
                <div className="flex flex-wrap gap-x-2.5 gap-y-0.5 text-[12.5px] text-faint">
                  {english && <span className="truncate">{english}</span>}
                  {isMovie ? (
                    <span>MOVIE · reserved — v1 tracks series</span>
                  ) : (
                    meta.map((m) => <span key={m}>{m}</span>)
                  )}
                </div>
              </ItemContent>
              <ItemActions>
                <Button
                  size="sm"
                  disabled={isMovie || add.isPending}
                  onClick={() => add.mutate(c)}
                >
                  {add.isPending && add.variables?.anilist_id === c.anilist_id && (
                    <Loader2 className="size-3.5 animate-spin" />
                  )}
                  Add
                </Button>
              </ItemActions>
            </Item>
          )
        })}
      </div>
    </div>
  )
}

export function AddSeriesButton() {
  const [open, setOpen] = useState(false)
  const isMobile = useIsMobile()
  const close = () => setOpen(false)

  const trigger = (
    <Button onClick={() => setOpen(true)}>
      <Plus className="size-4" /> Add series
    </Button>
  )

  if (isMobile) {
    return (
      <>
        {trigger}
        <Drawer open={open} onOpenChange={setOpen}>
          <DrawerContent className="px-4 pb-6">
            <DrawerHeader className="px-0">
              <DrawerTitle>Add series</DrawerTitle>
              <DrawerDescription>Search AniList and pick a title to track.</DrawerDescription>
            </DrawerHeader>
            <AddSeriesBody onDone={close} />
          </DrawerContent>
        </Drawer>
      </>
    )
  }

  return (
    <>
      {trigger}
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-w-xl">
          <DialogHeader>
            <DialogTitle>Add series</DialogTitle>
            <DialogDescription>Search AniList and pick a title to track.</DialogDescription>
          </DialogHeader>
          <AddSeriesBody onDone={close} />
        </DialogContent>
      </Dialog>
    </>
  )
}
