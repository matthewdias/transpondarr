import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Bot, Loader2 } from "lucide-react";
import { api, type Settings } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { Field, SectionShell } from "../section-shell";
import { useSaveToast } from "../section-helpers";

export function AutomationSection({ settings }: { settings: Settings }) {
  const a = settings.automation;
  const queryClient = useQueryClient();
  const [enabled, setEnabled] = useState(a.enabled);
  // Held as a string so the field can be transiently empty while being retyped.
  const [pinDelay, setPinDelay] = useState(String(a.pin_delay_hours));

  const saveToast = useSaveToast(queryClient, "Automation");
  const save = useMutation({
    mutationFn: () =>
      api.updateAutomation({
        enabled,
        pin_delay_hours: Math.max(0, Math.trunc(Number(pinDelay) || 0)),
      }),
    ...saveToast,
    onSuccess: (fresh) => {
      saveToast.onSuccess(fresh);
      // The service clamps the delay, so re-seed from what was actually saved.
      setPinDelay(String(fresh.automation.pin_delay_hours));
    },
  });

  return (
    <SectionShell
      icon={Bot}
      title="Automation"
      description="Whether Transpondarr searches and grabs on its own."
      footer={
        <Button
          size="sm"
          onClick={() => save.mutate()}
          disabled={save.isPending}
        >
          {save.isPending && <Loader2 className="size-4 animate-spin" />}
          Save
        </Button>
      }
    >
      <label className="flex cursor-pointer items-start justify-between gap-4">
        <span className="min-w-0">
          <span className="block text-xs font-medium">
            Automatic search and grab
          </span>
          <span className="mt-0.5 block text-[11px] text-faint">
            When off, the scheduler stops searching and grabbing entirely.
            Manual search and grab keep working. Off until you turn it on.
          </span>
        </span>
        <Switch
          checked={enabled}
          onCheckedChange={setEnabled}
          aria-label="Automatic search and grab"
        />
      </label>
      <Field
        label="Pinned group delay (hours)"
        type="number"
        min={0}
        max={24 * 365}
        value={pinDelay}
        onChange={(e) => setPinDelay(e.target.value)}
        hint="How long a series with a pinned group waits for that group before taking another's release. 0 takes the best release immediately; a series can override this."
      />
    </SectionShell>
  );
}
