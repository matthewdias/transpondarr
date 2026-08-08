import { useId, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Loader2, Trash2 } from "lucide-react";
import { ApiError, api, type UnmatchedDownload } from "@/lib/api";
import { activityUnmatchedQuery } from "@/lib/queries";
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

// Removing an unmatched download is destructive and manual by design: the
// payload may be exactly what someone was about to fix by hand, so nothing
// deletes it automatically.
export function RemoveUnmatchedDialog({ item }: { item: UnmatchedDownload }) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [deleteData, setDeleteData] = useState(true);
  const checkboxId = useId();

  const remove = useMutation({
    mutationFn: () => api.removeUnmatchedDownload(item.infohash, deleteData),
    onSuccess: () => {
      toast.success("Removed the download");
      void queryClient.invalidateQueries({
        queryKey: activityUnmatchedQuery().queryKey,
      });
      setOpen(false);
    },
    onError: (e) =>
      toast.error("Remove failed", {
        description: e instanceof ApiError ? e.message : String(e),
      }),
  });

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (o) setDeleteData(true);
      }}
    >
      <DialogTrigger asChild>
        <Button variant="outline" size="sm">
          <Trash2 className="size-3.5" aria-hidden />
          Remove
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Remove this download?</DialogTitle>
          <DialogDescription>
            Takes “{item.name}” out of the download client. No episode is
            waiting on it, and nothing in your library is touched.
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
            checked={deleteData}
            onChange={(e) => setDeleteData(e.target.checked)}
          />
          <span className="text-muted-foreground">
            Also delete the downloaded data from disk.
          </span>
        </label>
        <DialogFooter>
          <Button variant="outline" onClick={() => setOpen(false)}>
            Cancel
          </Button>
          <Button
            variant="destructive"
            onClick={() => remove.mutate()}
            disabled={remove.isPending}
          >
            {remove.isPending && <Loader2 className="size-4 animate-spin" />}
            Remove download
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
