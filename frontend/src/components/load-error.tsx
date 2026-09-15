import { RefreshCw, TriangleAlert } from "lucide-react";
import { errorReason } from "@/lib/api";
import { Button } from "@/components/ui/button";

/** Why a query failed, with a retry button, in the dashed card every failed load uses. */
export function LoadError({
  error,
  onRetry,
  ...props
}: ({ what: string } | { message: string }) & {
  error: unknown;
  onRetry: () => void;
}) {
  return (
    <div className="flex items-center gap-3 rounded-lg border border-dashed bg-card px-3.5 py-3">
      <TriangleAlert className="size-4 shrink-0 text-dl" />
      <p className="min-w-0 flex-1 text-sm text-muted-foreground">
        {"message" in props ? props.message : `Couldn’t load ${props.what}.`}{" "}
        {errorReason(error)}
      </p>
      <Button variant="outline" size="sm" onClick={onRetry}>
        <RefreshCw className="size-4" /> Try again
      </Button>
    </div>
  );
}
