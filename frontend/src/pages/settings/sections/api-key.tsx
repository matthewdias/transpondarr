import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { KeyRound, Loader2, Eye, EyeOff, Copy, RefreshCw } from "lucide-react";
import { api, type Settings } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SectionShell } from "../section-shell";

export function ApiKeySection({ settings }: { settings: Settings }) {
  const queryClient = useQueryClient();
  const [revealed, setRevealed] = useState(false);
  const key = settings.general.api_key;

  const regen = useMutation({
    mutationFn: api.regenerateApiKey,
    onSuccess: (newKey) => {
      queryClient.setQueryData<Settings>(["settings"], (old) =>
        old ? { ...old, general: { ...old.general, api_key: newKey } } : old,
      );
      toast.success("API key regenerated", {
        description: "Machine clients using the old key must be updated.",
      });
    },
    onError: (e) =>
      toast.error("Could not regenerate key", {
        description: e instanceof Error ? e.message : String(e),
      }),
  });

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(key);
      toast.success("API key copied");
    } catch {
      toast.error("Copy failed");
    }
  };

  return (
    <SectionShell
      icon={KeyRound}
      title="API access"
      description="For API clients (dashboards, scripts, a future HA integration). The web UI uses your login, not this key."
      footer={
        <Button
          variant="outline"
          size="sm"
          onClick={() => regen.mutate()}
          disabled={regen.isPending}
        >
          {regen.isPending ? (
            <Loader2 className="size-4 animate-spin" />
          ) : (
            <RefreshCw className="size-4" />
          )}
          Regenerate
        </Button>
      }
    >
      <label className="block">
        <span className="mb-1 block text-xs font-medium text-muted-foreground">
          API key
        </span>
        <div className="flex gap-2">
          <Input
            readOnly
            type={revealed ? "text" : "password"}
            value={key}
            className="font-mono"
          />
          <Button
            type="button"
            variant="outline"
            size="icon"
            className="flex-none"
            onClick={() => setRevealed((v) => !v)}
            aria-label={revealed ? "Hide API key" : "Reveal API key"}
          >
            {revealed ? (
              <EyeOff className="size-4" />
            ) : (
              <Eye className="size-4" />
            )}
          </Button>
          <Button
            type="button"
            variant="outline"
            size="icon"
            className="flex-none"
            onClick={copy}
            aria-label="Copy API key"
          >
            <Copy className="size-4" />
          </Button>
        </div>
      </label>
      <p className="text-[11px] text-faint">
        Send it as an <code className="font-mono">X-Api-Key</code> header.
        Regenerating invalidates the old key immediately.
      </p>
    </SectionShell>
  );
}
