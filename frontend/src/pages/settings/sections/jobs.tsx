import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ListChecks, Play } from "lucide-react";
import { toast } from "sonner";
import { api, type JobStatus } from "@/lib/api";
import { countdownOrDate, parseTimestamp, timeAgo } from "@/lib/format";
import { JOBS_POLL_MS, jobsQuery, settingsQuery } from "@/lib/queries";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import { SectionShell } from "../section-shell";

/** The runner's kebab-case identifier as prose: "wanted-search" → "Wanted search". */
function jobLabel(name: string): string {
  const words = name.replace(/[-_]/g, " ").trim();
  return words.charAt(0).toUpperCase() + words.slice(1);
}

/**
 * Duration at the precision that unit deserves. Only sub-millisecond runs keep
 * three decimals — they are the common case and the reason the API reports
 * fractional milliseconds at all, but "4.667 ms" is noise at every other scale.
 */
function jobDuration(ms: number): string {
  if (ms >= 1000) return `${(ms / 1000).toFixed(1)} s`;
  if (ms >= 10) return `${Math.round(ms)} ms`;
  if (ms >= 1) return `${Number(ms.toFixed(1))} ms`;
  return `${Number(ms.toFixed(3))} ms`;
}

// Two polls, because the snapshot on screen is already up to one poll old:
// import-scan runs every 15s, so without this every render would find its next
// run in the past and call a perfectly healthy runner overdue.
const OVERDUE_GRACE_MS = 2 * JOBS_POLL_MS;

/**
 * A next run well past due means the runner stopped scheduling — the failure
 * this card exists to catch, and one a relative last run cannot show without
 * the interval. Not while the job is running, though: the runner publishes
 * nextRun before a run starts, so a job outlasting its own interval is working
 * rather than late.
 */
function jobNextRun(j: JobStatus): { text: string; overdue: boolean } {
  if (!j.next_run) return { text: "—", overdue: false };
  const at = parseTimestamp(j.next_run);
  if (Number.isNaN(at)) return { text: "—", overdue: false };
  if (at > Date.now())
    return { text: countdownOrDate(j.next_run), overdue: false };
  if (j.running || Date.now() - at < OVERDUE_GRACE_MS) {
    return { text: "—", overdue: false };
  }
  return { text: "overdue", overdue: true };
}

export function JobsTable({
  jobs,
  onRun,
}: {
  jobs: JobStatus[];
  onRun?: (name: string) => void;
}) {
  if (jobs.length === 0) {
    return (
      <p className="text-xs text-muted-foreground">
        No background jobs are registered.
      </p>
    );
  }
  return (
    <ul className="divide-y">
      {jobs.map((j) => {
        const label = jobLabel(j.name);
        const next = jobNextRun(j);
        return (
          <li
            key={j.name}
            aria-label={label}
            className="py-2.5 first:pt-0 last:pb-0"
          >
            <div className="flex items-baseline justify-between gap-4">
              <span className="flex min-w-0 items-baseline gap-2 text-xs font-medium">
                <span className="truncate">{label}</span>
                {j.running && (
                  <span className="flex-none rounded-full bg-have-weak px-1.5 py-0.5 text-[10px] font-medium text-have">
                    Running
                  </span>
                )}
              </span>
              <span className="flex flex-none items-baseline gap-2 font-mono text-xs text-muted-foreground">
                <span
                  title={
                    j.last_run
                      ? `Last run took ${jobDuration(j.last_duration_ms)}`
                      : undefined
                  }
                >
                  {j.last_run ? timeAgo(j.last_run) : "Never"}
                </span>
                <span
                  className={next.overdue ? "text-destructive" : "text-faint"}
                >
                  {next.text}
                </span>
                {onRun && (
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    className="self-center"
                    aria-label={`Run ${label} now`}
                    // A trigger on a running job is only coalesced into the run
                    // already in flight, so there is nothing to queue.
                    disabled={j.running}
                    onClick={() => onRun(j.name)}
                  >
                    <Play />
                  </Button>
                )}
              </span>
            </div>
            {j.last_error && (
              <p className="mt-1 text-[11px] text-destructive">
                {j.last_error}
              </p>
            )}
          </li>
        );
      })}
    </ul>
  );
}

// The two jobs that grab unattended, and so the two the kill switch silences.
const AUTOMATION_GATED = ["wanted-search", "feed-poll"];

export function JobsSection() {
  const queryClient = useQueryClient();
  const { data, isLoading, isError, error } = useQuery(jobsQuery());
  const settings = useQuery(settingsQuery());
  const [confirming, setConfirming] = useState<string | null>(null);

  const run = useMutation({
    mutationFn: (name: string) => api.runJob(name),
    onSuccess: () => {
      // Fire and forget: the running pill and the new last run arrive on the poll.
      toast.success("Run queued");
      queryClient.invalidateQueries({ queryKey: jobsQuery().queryKey });
    },
    onError: (e) =>
      toast.error("Could not run the job", {
        description: e instanceof Error ? e.message : String(e),
      }),
  });

  // A failed settings read warns rather than assuming automation is on.
  const requiresConfirmation = (name: string) =>
    AUTOMATION_GATED.includes(name) && !settings.data?.automation.enabled;

  const handleRun = (name: string) => {
    if (requiresConfirmation(name)) {
      setConfirming(name);
      return;
    }
    run.mutate(name);
  };

  return (
    <SectionShell
      icon={ListChecks}
      title="Background jobs"
      description="What the scheduler has run, and whether it failed."
    >
      {isError && (
        <p className="text-xs text-destructive">
          Failed to load job status:{" "}
          {error instanceof Error ? error.message : String(error)}
        </p>
      )}
      {isLoading && (
        <div className="space-y-2.5">
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} className="h-5 w-full" />
          ))}
        </div>
      )}
      {data && (
        <JobsTable
          jobs={data}
          // Withheld until the kill switch is known, so the button never runs a
          // gated job without the warning it would have earned.
          onRun={settings.isPending ? undefined : handleRun}
        />
      )}
      {confirming && (
        <ConfirmGatedRunDialog
          name={confirming}
          onCancel={() => setConfirming(null)}
          onConfirm={() => {
            run.mutate(confirming);
            setConfirming(null);
          }}
        />
      )}
    </SectionShell>
  );
}

// Running a gated job by hand is explicit intent, so it bypasses the kill switch
// — and that is exactly why it is worth spelling out first.
function ConfirmGatedRunDialog({
  name,
  onCancel,
  onConfirm,
}: {
  name: string;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <Dialog open onOpenChange={(open) => !open && onCancel()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Run {jobLabel(name).toLowerCase()} anyway?</DialogTitle>
          <DialogDescription>
            Automation is off, so this job normally does nothing. Running it by
            hand searches your indexer and grabs whatever it picks, with no
            further prompt. Quality profiles, failure memory and pinned-group
            delays still apply.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="ghost" onClick={onCancel}>
            Cancel
          </Button>
          <Button onClick={onConfirm}>Run anyway</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
