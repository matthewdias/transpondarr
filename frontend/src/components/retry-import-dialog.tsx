import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";
import { RefreshCw, TriangleAlert } from "lucide-react";
import {
  api,
  type PayloadArchive,
  type PayloadFile,
  type QueueItem,
  errorReason,
} from "@/lib/api";
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

// parseSummary shows what the filename itself claimed, which is the whole reason
// the row needs a human: an empty summary is why nothing mapped it.
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

  const files = payload.data?.files ?? [];
  const archives: PayloadArchive[] = payload.data?.archives ?? [];

  // What the row shows is what the retry sends: an untouched suggestion left out
  // of the request would be re-derived, and overrides change how mapping runs.
  const selected = (file: PayloadFile) =>
    choices[file.path] ??
    (file.suggested_item > 0 ? String(file.suggested_item) : SKIP);

  const retry = useMutation({
    mutationFn: () =>
      api.retryQueueItemImport(
        item.id,
        files
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
      toast.error("Couldn’t import the files", {
        description: errorReason(e),
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
            {payload.isError
              ? "Nothing can be assigned until the files load."
              : files.length === 0 && archives.length > 0
                ? "Nothing here can be imported as it stands."
                : "Say which file is which episode. Anything left on “Skip” is not imported."}
          </DialogDescription>
        </DialogHeader>

        {payload.isLoading ? (
          <div className="space-y-2">
            <Skeleton className="h-10 w-full" />
            <Skeleton className="h-10 w-full" />
          </div>
        ) : payload.isError ? (
          // Plain text, not LoadError: a card inside a dialog is a box in a box.
          <div className="flex items-start gap-3">
            {/* Muted beside the amber triangle, as LoadError is: red is this app's
                colour for the library's own state, not for a load that failed. */}
            <TriangleAlert className="mt-0.5 size-4 shrink-0 text-dl" />
            <p className="min-w-0 flex-1 text-sm text-muted-foreground">
              Couldn’t list the files in this download.{" "}
              {errorReason(payload.error)}
            </p>
            <Button
              variant="outline"
              size="sm"
              onClick={() => void payload.refetch()}
            >
              <RefreshCw className="size-4" /> Try again
            </Button>
          </div>
        ) : (
          <div className="max-h-[50vh] space-y-3 overflow-y-auto">
            {archives.length > 0 ? (
              <div className="space-y-2 rounded-md border bg-panel-2 px-3 py-2">
                {archives.map((a) => (
                  <div key={a.path} className="min-w-0">
                    <div className="truncate font-mono text-xs">{a.path}</div>
                    <div className="text-xs text-faint">
                      {a.parts > 1
                        ? `archive set · ${a.parts} parts`
                        : "archive"}
                    </div>
                  </div>
                ))}
                <p className="text-sm text-muted-foreground">
                  Transpondarr does not unpack archives. Extract{" "}
                  {archives.length > 1 ? "them" : "it"} into the download
                  folder, then retry — the extracted episode will be listed
                  here.
                </p>
              </div>
            ) : null}

            {files.length > 0 ? (
              <ul className="space-y-2">
                {files.map((file) => (
                  <li
                    key={file.path}
                    className="flex items-center gap-3 rounded-md border bg-panel-2 px-3 py-2"
                  >
                    <div className="min-w-0 flex-1">
                      <div className="truncate font-mono text-xs">
                        {file.path}
                      </div>
                      <div className="text-xs text-faint">
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
                          <SelectItem
                            key={i.grab_id}
                            value={String(i.item_number)}
                          >
                            Episode {i.item_number}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </li>
                ))}
              </ul>
            ) : archives.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                This payload contains no video files.
              </p>
            ) : null}
          </div>
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
            {/* An unloaded payload has no files yet, which is not the same as
                having none — labelling it empty before it lands flickers the label. */}
            {retry.isPending
              ? "Importing…"
              : !payload.isPending && !payload.isError && files.length === 0
                ? "Retry import"
                : "Import"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
