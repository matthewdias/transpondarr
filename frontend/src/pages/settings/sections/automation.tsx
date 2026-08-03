import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Bot, Loader2 } from "lucide-react";
import { api, type Settings } from "@/lib/api";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Field, SectionShell } from "../section-shell";
import { useSaveToast } from "../section-helpers";

type Mode = Settings["automation"]["mode"];

const MODES: { value: Mode; label: string; hint: string }[] = [
  {
    value: "off",
    label: "Off",
    hint: "The scheduler stops searching and grabbing entirely. Manual search and grab keep working.",
  },
  {
    value: "notify_only",
    label: "Notify only",
    hint: "A rehearsal: automation searches and decides for real, sends a notification for what it would have grabbed (and why it would grab nothing), but nothing reaches the download client.",
  },
  {
    value: "on",
    label: "On",
    hint: "Automation searches and grabs on its own.",
  },
];

export function AutomationSection({ settings }: { settings: Settings }) {
  const a = settings.automation;
  const queryClient = useQueryClient();
  const [mode, setMode] = useState<Mode>(a.mode);
  // Held as a string so the field can be transiently empty while being retyped.
  const [pinDelay, setPinDelay] = useState(String(a.pin_delay_hours));

  const saveToast = useSaveToast(queryClient, "Automation");
  const save = useMutation({
    mutationFn: () =>
      api.updateAutomation({
        mode,
        pin_delay_hours: Math.max(0, Math.trunc(Number(pinDelay) || 0)),
      }),
    ...saveToast,
    onSuccess: (fresh) => {
      saveToast.onSuccess(fresh);
      // The service clamps the delay, so re-seed from what was actually saved.
      setPinDelay(String(fresh.automation.pin_delay_hours));
    },
  });

  const current = MODES.find((m) => m.value === mode) ?? MODES[0];

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
      <div className="space-y-1.5">
        <label
          className="block text-xs font-medium"
          htmlFor="automation-mode-trigger"
        >
          Automatic search and grab
        </label>
        <Select value={mode} onValueChange={(v) => setMode(v as Mode)}>
          <SelectTrigger
            id="automation-mode-trigger"
            aria-label="Automatic search and grab"
            className="w-full"
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {MODES.map((m) => (
              <SelectItem key={m.value} value={m.value}>
                {m.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <p className="text-[11px] text-faint">
          {current.hint} Off until you turn it on.
        </p>
      </div>
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
