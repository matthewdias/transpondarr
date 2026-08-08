import { useState } from "react";
import { useNavigate } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Plus, RefreshCw, Search, Loader2, TriangleAlert } from "lucide-react";
import { api, ApiError, type Candidate, type MonitorItems } from "@/lib/api";
import { metadataSearchQuery, seriesQuery } from "@/lib/queries";
import { useDebounce } from "@/hooks/use-debounce";
import { useIsMobile } from "@/hooks/use-mobile";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
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
} from "@/components/ui/item";

function candidateTitle(c: Candidate) {
  return c.romaji || c.english || c.native || `${c.provider} ${c.provider_id}`;
}

// Before the add, not after: a new series sorts to the front of the sweep queue
// and one pass grabs everything eligible.
const monitorChoices: { value: MonitorItems; label: string; hint: string }[] = [
  { value: "all", label: "All episodes", hint: "Including the back catalogue" },
  {
    value: "future",
    label: "Future only",
    hint: "From the next broadcast onwards",
  },
];

// The mode control is far from the button, so the button carries the
// consequence. Only a departure from the default is worth saying.
const monitorAnnotation: Record<MonitorItems, string> = {
  all: "",
  future: "future only",
};

function AddSeriesBody({ onDone }: { onDone: () => void }) {
  const [term, setTerm] = useState("");
  const [monitorItems, setMonitorItems] = useState<MonitorItems>("all");
  const debounced = useDebounce(term, 350);
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  const query = debounced.trim();

  const search = useQuery({
    ...metadataSearchQuery(query),
    enabled: query.length > 0,
  });

  const add = useMutation({
    mutationFn: (c: Candidate) =>
      api.addSeries(c.provider, c.provider_id, monitorItems),
    onSuccess: (series) => {
      queryClient.invalidateQueries({ queryKey: seriesQuery().queryKey });
      toast.success("Series added", {
        description: `${series.title} — ${series.items.length} wanted items expanded`,
      });
      onDone();
      navigate(`/series/${series.id}`);
    },
    onError: (err, c) => {
      if (err instanceof ApiError && err.status === 409) {
        toast.info("Already tracking", { description: candidateTitle(c) });
        onDone();
        return;
      }
      toast.error("Could not add series", {
        description: err instanceof Error ? err.message : String(err),
      });
    },
  });

  const results = search.data ?? [];
  const annotation = monitorAnnotation[monitorItems];
  const addLabel = annotation ? `Add · ${annotation}` : "Add";

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

      <div className="mt-3 flex items-center gap-2">
        <span className="text-[13px] text-muted-foreground">
          Monitor on add
        </span>
        <Select
          value={monitorItems}
          onValueChange={(v) => setMonitorItems(v as MonitorItems)}
        >
          <SelectTrigger size="sm" aria-label="Monitor on add">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {monitorChoices.map((c) => (
              <SelectItem key={c.value} value={c.value} description={c.hint}>
                {c.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <div className="mt-2 max-h-[56vh] min-h-[8rem] overflow-y-auto">
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
          const isMovie = c.format === "MOVIE";
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
                  // Every row's visible label is identical, so the accessible
                  // name carries the title.
                  aria-label={
                    annotation
                      ? `Add ${candidateTitle(c)} · ${annotation}`
                      : `Add ${candidateTitle(c)}`
                  }
                  onClick={() => add.mutate(c)}
                >
                  {add.isPending &&
                    add.variables?.provider === c.provider &&
                    add.variables?.provider_id === c.provider_id && (
                      <Loader2 className="size-3.5 animate-spin" />
                    )}
                  <span className="whitespace-nowrap">{addLabel}</span>
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
  const isMobile = useIsMobile();
  const close = () => setOpen(false);

  const trigger = (
    <Button onClick={() => setOpen(true)}>
      <Plus className="size-4" /> Add series
    </Button>
  );

  if (isMobile) {
    return (
      <>
        {trigger}
        <Drawer open={open} onOpenChange={setOpen}>
          <DrawerContent className="px-4 pb-6">
            <DrawerHeader className="px-0">
              <DrawerTitle>Add series</DrawerTitle>
              <DrawerDescription>
                Search AniList and pick a title to track.
              </DrawerDescription>
            </DrawerHeader>
            <AddSeriesBody onDone={close} />
          </DrawerContent>
        </Drawer>
      </>
    );
  }

  return (
    <>
      {trigger}
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-w-xl">
          <DialogHeader>
            <DialogTitle>Add series</DialogTitle>
            <DialogDescription>
              Search AniList and pick a title to track.
            </DialogDescription>
          </DialogHeader>
          <AddSeriesBody onDone={close} />
        </DialogContent>
      </Dialog>
    </>
  );
}
