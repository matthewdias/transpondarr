import { Loader2, CheckCircle2, XCircle, Plug } from "lucide-react";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

export type TestState = { ok: boolean; message: string } | null;

export function SectionShell({
  icon: Icon,
  title,
  description,
  configured,
  children,
  footer,
}: {
  icon: React.ComponentType<{ className?: string }>;
  title: string;
  description: string;
  configured?: boolean;
  children: React.ReactNode;
  footer?: React.ReactNode;
}) {
  return (
    <section className="overflow-hidden rounded-lg border bg-card shadow-sm">
      <header className="flex items-start gap-3 border-b px-4 py-3.5">
        <span className="mt-0.5 grid size-8 flex-none place-items-center rounded-md bg-panel-2 text-muted-foreground">
          <Icon className="size-4" />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <h2 className="text-sm font-semibold">{title}</h2>
            {configured != null && (
              <span
                className={cn(
                  "rounded-full border px-2 py-0.5 text-[11px] font-medium",
                  configured
                    ? "border-transparent bg-have-weak text-have"
                    : "border-border bg-panel-2 text-faint",
                )}
              >
                {configured ? "Configured" : "Not configured"}
              </span>
            )}
          </div>
          <p className="mt-0.5 text-xs text-muted-foreground">{description}</p>
        </div>
      </header>
      <div className="space-y-3.5 px-4 py-4">{children}</div>
      {footer && (
        <footer className="flex items-center gap-2 border-t bg-panel-2/40 px-4 py-3">
          {footer}
        </footer>
      )}
    </section>
  );
}

export function Field({
  label,
  hint,
  ...props
}: React.ComponentProps<typeof Input> & { label: string; hint?: string }) {
  return (
    <label className="block">
      <span className="mb-1 block text-xs font-medium text-muted-foreground">
        {label}
      </span>
      <Input {...props} />
      {hint && (
        <span className="mt-1 block text-[11px] text-faint">{hint}</span>
      )}
    </label>
  );
}

/** Test + Save footer with an inline result line. */
export function SectionFooter({
  onTest,
  onSave,
  testing,
  saving,
  testState,
}: {
  onTest: () => void;
  onSave: () => void;
  testing: boolean;
  saving: boolean;
  testState: TestState;
}) {
  return (
    <>
      <Button
        variant="outline"
        size="sm"
        onClick={onTest}
        disabled={testing || saving}
      >
        {testing ? (
          <Loader2 className="size-4 animate-spin" />
        ) : (
          <Plug className="size-4" />
        )}
        Test
      </Button>
      <Button size="sm" onClick={onSave} disabled={saving || testing}>
        {saving && <Loader2 className="size-4 animate-spin" />}
        Save
      </Button>
      {testState && (
        <span
          className={cn(
            "ml-1 flex min-w-0 items-center gap-1.5 text-xs",
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
    </>
  );
}
