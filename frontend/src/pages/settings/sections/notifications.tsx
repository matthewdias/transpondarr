import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Bell, CheckCircle2, Loader2, Plug, XCircle } from "lucide-react";
import { api, type NotificationsInput, type Settings } from "@/lib/api";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { Field, SectionShell, type TestState } from "../section-shell";
import { useSaveToast } from "../section-helpers";

// One canonical display name per adapter, shared by headings, switch
// aria-labels, and Test buttons (ntfy is a lowercase brand).
const ADAPTER_DISCORD = "Discord";
const ADAPTER_WEBHOOK = "Webhook";
const ADAPTER_NTFY = "ntfy";

type EventToggles = {
  on_grabbed: boolean;
  on_imported: boolean;
  on_stuck: boolean;
  on_grab_failed: boolean;
  on_series_added: boolean;
  on_rehearsal: boolean;
};

const EVENT_ROWS: { key: keyof EventToggles; label: string; hint: string }[] = [
  { key: "on_grabbed", label: "Grabbed", hint: "Automation grabbed a release" },
  {
    key: "on_imported",
    label: "Imported",
    hint: "An episode landed in the library",
  },
  { key: "on_stuck", label: "Import stuck", hint: "An import cannot proceed" },
  {
    key: "on_grab_failed",
    label: "Grab failed",
    hint: "A download errored or vanished",
  },
  { key: "on_series_added", label: "Series added", hint: "A series was added" },
  {
    key: "on_rehearsal",
    label: "Rehearsal",
    hint: "What notify-only automation would have done",
  },
];

function toggles(t: EventToggles): EventToggles {
  return {
    on_grabbed: t.on_grabbed,
    on_imported: t.on_imported,
    on_stuck: t.on_stuck,
    on_grab_failed: t.on_grab_failed,
    on_series_added: t.on_series_added,
    on_rehearsal: t.on_rehearsal,
  };
}

/** Per-event switch rows, aria-labelled "<adapter> <event>". */
function EventSwitches({
  adapter,
  value,
  onChange,
}: {
  adapter: string;
  value: EventToggles;
  onChange: (next: EventToggles) => void;
}) {
  return (
    <div className="grid gap-2 sm:grid-cols-2">
      {EVENT_ROWS.map((row) => (
        <label
          key={row.key}
          className="flex cursor-pointer items-center justify-between gap-3 rounded-md border px-3 py-2"
        >
          <span className="min-w-0">
            <span className="block text-xs font-medium">{row.label}</span>
            <span className="mt-0.5 block text-[11px] text-faint">
              {row.hint}
            </span>
          </span>
          <Switch
            checked={value[row.key]}
            onCheckedChange={(checked) =>
              onChange({ ...value, [row.key]: checked })
            }
            aria-label={`${adapter} ${row.label}`}
          />
        </label>
      ))}
    </div>
  );
}

/** Per-adapter Test button with its own inline result line. */
function TestButton({
  adapter,
  onTest,
  testing,
  testState,
}: {
  adapter: string;
  onTest: () => void;
  testing: boolean;
  testState: TestState;
}) {
  return (
    <div className="flex items-center gap-2">
      <Button variant="outline" size="sm" onClick={onTest} disabled={testing}>
        {testing ? (
          <Loader2 className="size-4 animate-spin" />
        ) : (
          <Plug className="size-4" />
        )}
        Test {adapter}
      </Button>
      {testState && (
        <span
          className={cn(
            "flex min-w-0 items-center gap-1.5 text-xs",
            testState.ok ? "text-have" : "text-destructive",
          )}
        >
          {testState.ok ? (
            <CheckCircle2 className="size-3.5 flex-none" />
          ) : (
            <XCircle className="size-3.5 flex-none" />
          )}
          <span className="truncate">{testState.message}</span>
        </span>
      )}
    </div>
  );
}

function useTest(mutationFn: () => Promise<unknown>) {
  const [state, setState] = useState<TestState>(null);
  const test = useMutation({
    mutationFn,
    onSuccess: () => setState({ ok: true, message: "Test notification sent." }),
    onError: (e) =>
      setState({
        ok: false,
        message: e instanceof Error ? e.message : String(e),
      }),
  });
  return { state, test };
}

export function NotificationsSection({ settings }: { settings: Settings }) {
  const n = settings.notifications;
  const queryClient = useQueryClient();

  const [discordUrl, setDiscordUrl] = useState(n.discord.url);
  const [discordEvents, setDiscordEvents] = useState(toggles(n.discord));
  const [webhookUrl, setWebhookUrl] = useState(n.webhook.url);
  const [webhookEvents, setWebhookEvents] = useState(toggles(n.webhook));
  const [ntfyServer, setNtfyServer] = useState(n.ntfy.server);
  const [ntfyTopic, setNtfyTopic] = useState(n.ntfy.topic);
  const [ntfyToken, setNtfyToken] = useState("");
  const [ntfyEvents, setNtfyEvents] = useState(toggles(n.ntfy));

  const body = (): NotificationsInput => ({
    discord: { url: discordUrl, ...discordEvents },
    webhook: { url: webhookUrl, ...webhookEvents },
    ntfy: {
      server: ntfyServer,
      topic: ntfyTopic,
      token: ntfyToken || undefined,
      ...ntfyEvents,
    },
  });

  const discordTest = useTest(() => api.testNotifyDiscord(body()));
  const webhookTest = useTest(() => api.testNotifyWebhook(body()));
  const ntfyTest = useTest(() => api.testNotifyNtfy(body()));

  const save = useMutation({
    mutationFn: () => api.updateNotifications(body()),
    ...useSaveToast(queryClient, "Notifications"),
  });

  return (
    <SectionShell
      icon={Bell}
      title="Notifications"
      description="Push pipeline events to Discord, a webhook, or ntfy."
      configured={
        n.discord.configured || n.webhook.configured || n.ntfy.configured
      }
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
      <div className="space-y-3">
        <h3 className="text-xs font-semibold">{ADAPTER_DISCORD}</h3>
        <Field
          label="Discord webhook URL"
          placeholder="https://discord.com/api/webhooks/…"
          value={discordUrl}
          onChange={(e) => setDiscordUrl(e.target.value)}
          hint="Leave empty to disable Discord notifications."
        />
        <EventSwitches
          adapter={ADAPTER_DISCORD}
          value={discordEvents}
          onChange={setDiscordEvents}
        />
        <TestButton
          adapter={ADAPTER_DISCORD}
          onTest={() => discordTest.test.mutate()}
          testing={discordTest.test.isPending}
          testState={discordTest.state}
        />
      </div>

      <div className="space-y-3 border-t pt-3.5">
        <h3 className="text-xs font-semibold">{ADAPTER_WEBHOOK}</h3>
        <Field
          label="Webhook URL"
          placeholder="https://example.com/hook"
          value={webhookUrl}
          onChange={(e) => setWebhookUrl(e.target.value)}
          hint="POSTs a stable JSON payload you can script against. Leave empty to disable."
        />
        <EventSwitches
          adapter={ADAPTER_WEBHOOK}
          value={webhookEvents}
          onChange={setWebhookEvents}
        />
        <TestButton
          adapter={ADAPTER_WEBHOOK}
          onTest={() => webhookTest.test.mutate()}
          testing={webhookTest.test.isPending}
          testState={webhookTest.state}
        />
      </div>

      <div className="space-y-3 border-t pt-3.5">
        <h3 className="text-xs font-semibold">{ADAPTER_NTFY}</h3>
        <div className="grid gap-3.5 sm:grid-cols-2">
          <Field
            label="ntfy server"
            placeholder="https://ntfy.sh"
            value={ntfyServer}
            onChange={(e) => setNtfyServer(e.target.value)}
          />
          <Field
            label="ntfy topic"
            value={ntfyTopic}
            onChange={(e) => setNtfyTopic(e.target.value)}
            hint="Leave empty to disable ntfy notifications."
          />
        </div>
        <Field
          label="ntfy access token"
          type="password"
          value={ntfyToken}
          onChange={(e) => setNtfyToken(e.target.value)}
          placeholder={n.ntfy.token_set ? "•••••••• (unchanged)" : ""}
          hint={
            n.ntfy.token_set
              ? "Leave blank to keep the stored token."
              : "Optional; only needed for protected topics."
          }
          autoComplete="new-password"
        />
        <EventSwitches
          adapter={ADAPTER_NTFY}
          value={ntfyEvents}
          onChange={setNtfyEvents}
        />
        <TestButton
          adapter={ADAPTER_NTFY}
          onTest={() => ntfyTest.test.mutate()}
          testing={ntfyTest.test.isPending}
          testState={ntfyTest.state}
        />
      </div>
    </SectionShell>
  );
}
