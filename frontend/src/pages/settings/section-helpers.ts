import { toast } from "sonner";
import type { useQueryClient } from "@tanstack/react-query";
import { type Settings } from "@/lib/api";
import { settingsQuery } from "@/lib/queries";

/** Shared styling for the native <select> elements used across sections. */
export const selectClass =
  "flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm outline-none focus-visible:ring-2 focus-visible:ring-ring";

export function useSaveToast(
  queryClient: ReturnType<typeof useQueryClient>,
  label: string,
) {
  return {
    onSuccess: (fresh: Settings) => {
      queryClient.setQueryData(settingsQuery().queryKey, fresh);
      toast.success(`${label} saved`, {
        description: "Applied live — no restart needed.",
      });
    },
    onError: (err: unknown) =>
      toast.error(`Could not save ${label.toLowerCase()}`, {
        description: err instanceof Error ? err.message : String(err),
      }),
  };
}
