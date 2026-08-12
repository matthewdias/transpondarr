import { useState } from "react";
import { useNavigate } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Plus, RefreshCw, Search, Loader2, TriangleAlert } from "lucide-react";
import { ApiError, type Candidate } from "@/lib/api";
import { metadataSearchQuery } from "@/lib/queries";
import { useDebounce } from "@/hooks/use-debounce";
import { useIsMobile } from "@/hooks/use-mobile";
import { AddTitleForm } from "@/components/add-title-form";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Poster } from "@/components/poster";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import {
  Drawer,
  DrawerContent,
  DrawerHeader,
  DrawerTitle,
  DrawerDescription,
} from "@/components/ui/drawer";
import {
  Item,
  ItemContent,
  ItemMedia,
  ItemActions,
  ItemTitle,
} from "@/components/ui/item";

function candidateTitle(c: Candidate) {
  return c.romaji || c.english || c.native || `${c.provider} ${c.provider_id}`;
}

// selected is the container's, not this body's: Escape is dispatched on the
// dialog, so only the container can turn it into a step back.
function AddSeriesBody({
  onDone,
  selected,
  onSelect,
}: {
  onDone: () => void;
  selected: Candidate | null;
  onSelect: (candidate: Candidate | null) => void;
}) {
  const [term, setTerm] = useState("");
  const debounced = useDebounce(term, 350);
  const navigate = useNavigate();

  const query = debounced.trim();

  const search = useQuery({
    ...metadataSearchQuery(query),
    enabled: query.length > 0,
  });

  const results = search.data ?? [];

  // A step rather than a stacked dialog: this body stays mounted, so Back
  // returns to the search term and results that led here.
  if (selected) {
    return (
      <div>
        {/* The dialog header names the flow, so the step names the title -- as
            the same row it was picked from, which is the continuity a step
            owes the list behind it. */}
        <Item variant="muted" size="sm" className="mb-3 gap-3">
          <ItemMedia>
            <Poster
              title={candidateTitle(selected)}
              coverUrl={selected.cover_url}
            />
          </ItemMedia>
          <ItemContent className="min-w-0">
            <ItemTitle className="max-w-full truncate">
              {candidateTitle(selected)}
            </ItemTitle>
          </ItemContent>
        </Item>
        <AddTitleForm
          key={`${selected.provider}:${selected.provider_id}`}
          title={candidateTitle(selected)}
          target={selected}
          onBack={() => onSelect(null)}
          onAdded={(series) => {
            onDone();
            navigate(`/series/${series.id}`);
          }}
          onExists={onDone}
        />
      </div>
    );
  }

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

      <div className="mt-3 max-h-[56vh] min-h-[8rem] overflow-y-auto">
        {/* A paused retry (browser offline) reports neither fetching nor error. */}
        {(search.isFetching || search.isPaused) && (
          <div className="flex items-center justify-center gap-2 py-10 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" /> Searching…
          </div>
        )}

        {!search.isFetching && !search.isPaused && search.isError && (
          <div className="flex flex-col items-center px-4 py-8 text-center">
            <TriangleAlert className="mb-3 size-6 text-dl" />
            <h3 className="text-sm font-semibold">Couldn’t search AniList</h3>
            <p className="mt-1.5 max-w-sm text-sm text-muted-foreground">
              {search.error instanceof ApiError
                ? search.error.message
                : String(search.error)}
            </p>
            <Button
              variant="outline"
              size="sm"
              className="mt-4"
              onClick={() => search.refetch()}
            >
              <RefreshCw className="size-4" /> Try again
            </Button>
          </div>
        )}

        {!search.isFetching && search.isSuccess && results.length === 0 && (
          <p className="py-10 text-center text-sm text-muted-foreground">
            No titles found for “{query}”.
          </p>
        )}

        {!query && (
          <p className="py-10 text-center text-sm text-faint">
            Type a title to search AniList.
          </p>
        )}

        {results.map((c) => {
          const meta = [
            c.format && `${c.format}${c.episodes ? ` · ${c.episodes} ep` : ""}`,
            [c.year, c.status].filter(Boolean).join(" · ") || null,
          ].filter(Boolean) as string[];
          const english =
            c.english && c.english !== candidateTitle(c) ? c.english : null;
          return (
            <Item key={`${c.provider}:${c.provider_id}`} className="gap-3">
              <ItemMedia>
                <Poster title={candidateTitle(c)} coverUrl={c.cover_url} />
              </ItemMedia>
              <ItemContent className="min-w-0 gap-0.5">
                <div className="truncate text-sm font-medium">
                  {candidateTitle(c)}
                </div>
                <div className="flex flex-wrap gap-x-2.5 gap-y-0.5 text-[12.5px] text-faint">
                  {english && <span className="truncate">{english}</span>}
                  {meta.map((m) => (
                    <span key={m}>{m}</span>
                  ))}
                </div>
              </ItemContent>
              <ItemActions>
                <Button
                  size="sm"
                  // Every row's visible label is identical, so the accessible
                  // name carries the title.
                  aria-label={`Add ${candidateTitle(c)}`}
                  onClick={() => onSelect(c)}
                >
                  Add
                </Button>
              </ItemActions>
            </Item>
          );
        })}
      </div>
    </div>
  );
}

export function AddSeriesButton() {
  const [open, setOpen] = useState(false);
  const [selected, setSelected] = useState<Candidate | null>(null);
  const isMobile = useIsMobile();
  // The next open starts at the search; the closed dialog keeps no step.
  const setDialogOpen = (next: boolean) => {
    setOpen(next);
    if (!next) setSelected(null);
  };
  const close = () => setDialogOpen(false);

  // Escape leaves the step, not the whole dialog: the form is a layer over the
  // results, and the stacked dialog it replaced would have peeled off one at a
  // time. Radix dispatches this from a capture-phase document listener, so
  // preventing it here is the only way to intercept it.
  const stepBackOnEscape = (event: KeyboardEvent) => {
    if (!selected) return;
    event.preventDefault();
    setSelected(null);
  };

  const body = (
    <AddSeriesBody onDone={close} selected={selected} onSelect={setSelected} />
  );

  const trigger = (
    <Button onClick={() => setOpen(true)}>
      <Plus className="size-4" /> Add series
    </Button>
  );

  if (isMobile) {
    return (
      <>
        {trigger}
        <Drawer open={open} onOpenChange={setDialogOpen}>
          <DrawerContent
            className="px-4 pb-6"
            onEscapeKeyDown={stepBackOnEscape}
          >
            <DrawerHeader className="px-0">
              <DrawerTitle>Add series</DrawerTitle>
              <DrawerDescription>
                Search AniList and pick a title to track.
              </DrawerDescription>
            </DrawerHeader>
            {body}
          </DrawerContent>
        </Drawer>
      </>
    );
  }

  return (
    <>
      {trigger}
      <Dialog open={open} onOpenChange={setDialogOpen}>
        <DialogContent className="max-w-xl" onEscapeKeyDown={stepBackOnEscape}>
          <DialogHeader>
            <DialogTitle>Add series</DialogTitle>
            <DialogDescription>
              Search AniList and pick a title to track.
            </DialogDescription>
          </DialogHeader>
          {body}
        </DialogContent>
      </Dialog>
    </>
  );
}
