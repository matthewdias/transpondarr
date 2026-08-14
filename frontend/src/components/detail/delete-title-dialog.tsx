import { useId, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Loader2, Trash2 } from "lucide-react";
import { api, ApiError, type TitleDetail } from "@/lib/api";
import { titleDetailQuery, titlesQuery } from "@/lib/queries";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";

// The delete confirmation: says in counts what goes (tracking, history,
// blocklist memory) and what stays (library files, always). The checkbox is the
// only way downloads are touched, and it takes their data with them.
export function DeleteTitleDialog({
  detail,
  onDeleted,
}: {
  detail: TitleDetail;
  onDeleted: () => void;
}) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [removeDownloads, setRemoveDownloads] = useState(false);
  const checkboxId = useId();

  const tracked = detail.items.length;
  const inLibrary = detail.items.filter(
    (i) => i.status === "in_library",
  ).length;
  const active = detail.items.filter(
    (i) =>
      i.status === "downloading" ||
      i.status === "stuck" ||
      i.status === "deferred",
  ).length;

  const del = useMutation({
    mutationFn: () => api.deleteTitle(detail.id, removeDownloads),
    onSuccess: () => {
      toast.success(`Deleted “${detail.title}”`);
      // Drop the still-mounted detail query first, or the title-prefix
      // invalidation refetches it into a 404 before the navigation lands.
      queryClient.removeQueries({
        queryKey: titleDetailQuery(detail.id).queryKey,
      });
      // The calendar caches its own title-derived rows under a separate prefix.
      queryClient.invalidateQueries({ queryKey: titlesQuery().queryKey });
      queryClient.invalidateQueries({ queryKey: ["calendar"] });
      setOpen(false);
      onDeleted();
    },
    onError: (e) =>
      toast.error("Delete failed", {
        description: e instanceof ApiError ? e.message : String(e),
      }),
  });

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (o) setRemoveDownloads(false);
      }}
    >
      <DialogTrigger asChild>
        <Button variant="outline" size="sm">
          <Trash2 className="size-3.5" aria-hidden />
          Delete
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Delete “{detail.title}”?</DialogTitle>
          <DialogDescription>
            Removes {tracked} tracked episodes, grab history, and blocklist
            memory from Transpondarr.{" "}
            {inLibrary > 0
              ? `The ${inLibrary} ${inLibrary === 1 ? "episode" : "episodes"} in your library ${inLibrary === 1 ? "stays" : "stay"} on disk — library files are never touched.`
              : "Library files are never touched."}
          </DialogDescription>
        </DialogHeader>
        <label
          htmlFor={checkboxId}
          className="flex cursor-pointer items-start gap-2.5 rounded-md border border-border bg-panel-2 px-3 py-2.5 text-sm"
        >
          <input
            id={checkboxId}
            type="checkbox"
            className="mt-0.5 size-4 flex-none accent-destructive"
            checked={removeDownloads}
            onChange={(e) => setRemoveDownloads(e.target.checked)}
          />
          <span className="text-muted-foreground">
            Also remove its torrents and their downloaded data from the download
            client
            {active > 0 && (
              <>
                {" "}
                — {active} active {active === 1 ? "download" : "downloads"}
              </>
            )}
            , including any left seeding.
          </span>
        </label>
        <DialogFooter>
          <Button variant="outline" onClick={() => setOpen(false)}>
            Cancel
          </Button>
          <Button
            variant="destructive"
            onClick={() => del.mutate()}
            disabled={del.isPending}
          >
            {del.isPending && <Loader2 className="size-4 animate-spin" />}
            {detail.format === "MOVIE" ? "Delete film" : "Delete series"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
