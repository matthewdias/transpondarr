import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";
import { ApiError, api, type PayloadFile, type QueueItem } from "@/lib/api";
import {
  activityHistoryQuery,
  activityQueueQuery,
  queueItemPayloadQuery,
} from "@/lib/queries";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";

// SKIP is the "leave this file alone" choice. Radix Select has no empty-string
// value, so the sentinel is a token rather than "".
const SKIP = "skip";

// parseSummary says what the filename itself claimed, which is the whole reason
// the row needs a human: an empty summary is exactly why nothing mapped it.
function parseSummary(file: PayloadFile): string {
  const bits: string[] = [];
  if (file.batch) bits.push("batch");
  if (file.episode_start > 0) {
    bits.push(
      file.episode_end > file.episode_start
        ? `episodes ${file.episode_start}–${file.episode_end}`
        : `episode ${file.episode_start}`,
    );
  }
  if (file.absolute_episode > 0) bits.push(`absolute ${file.absolute_episode}`);
  if (file.version > 1) bits.push(`v${file.version}`);
  if (file.repack) bits.push("repack");
  return bits.length > 0 ? bits.join(" · ") : "no episode number read";
}

export function RetryImportDialog({
  item,
  open,
  onOpenChange,
}: {
  item: QueueItem;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const queryClient = useQueryClient();
  const [choices, setChoices] = useState<Record<string, string>>({});
  const payload = useQuery({
    ...queueItemPayloadQuery(item.id),
    enabled: open,
  });

  // What the row shows is what the retry sends: an untouched suggestion left out
  // of the request would be re-derived, and overrides change how mapping runs.
  const selected = (file: PayloadFile) =>
    choices[file.path] ??
    (file.suggested_item > 0 ? String(file.suggested_item) : SKIP);

  const retry = useMutation({
    mutationFn: () =>
      api.retryQueueItemImport(
        item.id,
        (payload.data?.files ?? [])
          .map((file) => ({ file: file.path, choice: selected(file) }))
          .filter(({ choice }) => choice !== SKIP)
          .map(({ file, choice }) => ({ file, item_number: Number(choice) })),
      ),
    onSuccess: (results) => {
      const imported = results.filter((r) => r.outcome === "imported").length;
      if (imported > 0) {
        toast.success(
          imported === 1
            ? "Imported 1 episode"
            : `Imported ${imported} episodes`,
        );
      } else {
        toast.warning("Nothing could be imported", {
          description: results[0]?.detail || undefined,
        });
      }
      void queryClient.invalidateQueries({
        queryKey: activityQueueQuery().queryKey,
      });
      void queryClient.invalidateQueries({
        queryKey: activityHistoryQuery().queryKey,
      });
      onOpenChange(false);
    },
    onError: (e) =>
      toast.error("Import fix failed", {
        description: e instanceof Error ? e.message : String(e),
      }),
  });

  // Only rows still awaiting a fix can take a file; anything else already has one.
  const unfilled = (payload.data?.items ?? []).filter(
    (i) => i.status === "import_deferred",
  );

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Fix import</DialogTitle>
          <DialogDescription>
            Say which file is which episode. Anything left on “Skip” is not
            imported.
          </DialogDescription>
        </DialogHeader>

        {payload.isLoading ? (
          <div className="space-y-2">
            <Skeleton className="h-10 w-full" />
            <Skeleton className="h-10 w-full" />
          </div>
        ) : payload.isError ? (
          <p className="text-[13px] text-destructive">
            {payload.error instanceof ApiError
              ? payload.error.message
              : String(payload.error)}
          </p>
        ) : payload.data?.files.length === 0 ? (
          <p className="text-[13px] text-muted-foreground">
            This payload holds no video files.
          </p>
        ) : (
          <ul className="max-h-[50vh] space-y-2 overflow-y-auto">
            {payload.data?.files.map((file) => (
              <li
                key={file.path}
                className="flex items-center gap-3 rounded-md border bg-panel-2 px-3 py-2"
              >
                <div className="min-w-0 flex-1">
                  <div className="truncate font-mono text-[12px]">
                    {file.path}
                  </div>
                  <div className="text-[12px] text-faint">
                    {parseSummary(file)}
                  </div>
                </div>
                <Select
                  value={selected(file)}
                  onValueChange={(v) =>
                    setChoices((prev) => ({ ...prev, [file.path]: v }))
                  }
                >
                  <SelectTrigger
                    size="sm"
                    aria-label={`Episode for ${file.path}`}
                    className="w-36"
                  >
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value={SKIP}>Skip</SelectItem>
                    {unfilled.map((i) => (
                      <SelectItem key={i.grab_id} value={String(i.item_number)}>
                        Episode {i.item_number}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </li>
            ))}
          </ul>
        )}

        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={retry.isPending}
          >
            Cancel
          </Button>
          <Button
            onClick={() => retry.mutate()}
            disabled={retry.isPending || !payload.data}
          >
            {retry.isPending ? "Importing…" : "Import"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
