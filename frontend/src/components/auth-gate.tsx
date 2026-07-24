import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Snail, User, KeyRound, Loader2 } from "lucide-react";
import { api, AUTH_EXPIRED_EVENT, UnauthorizedError } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

type Phase = "loading" | "setup" | "login" | "ready";

/**
 * Forms-based auth. On load we ask the server for auth status: an
 * authenticated request (session cookie, API key, or local-address bypass) goes
 * straight through; an unconfigured server shows first-run setup; otherwise a
 * login screen. The httpOnly session cookie carries auth thereafter — no token
 * is ever held in JS. A 401 re-runs the check.
 */
export function AuthGate({ children }: { children: React.ReactNode }) {
  const [phase, setPhase] = useState<Phase>("loading");
  const queryClient = useQueryClient();

  useEffect(() => {
    if (phase !== "loading") return;
    let cancelled = false;
    void (async () => {
      try {
        const s = await api.authStatus();
        if (cancelled) return;
        setPhase(s.authenticated ? "ready" : s.configured ? "login" : "setup");
      } catch {
        if (!cancelled) setPhase("login");
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [phase]);

  useEffect(() => {
    const onExpired = () => setPhase("loading");
    window.addEventListener(AUTH_EXPIRED_EVENT, onExpired);
    return () => window.removeEventListener(AUTH_EXPIRED_EVENT, onExpired);
  }, []);

  const onAuthed = async () => {
    await queryClient.invalidateQueries();
    setPhase("ready");
  };

  if (phase === "ready") return <>{children}</>;

  if (phase === "loading") {
    return (
      <div className="flex min-h-svh items-center justify-center bg-background text-muted-foreground">
        <Loader2 className="size-5 animate-spin" />
      </div>
    );
  }

  return (
    <AuthShell
      subtitle={
        phase === "setup"
          ? "Create your admin account."
          : "Sign in to continue."
      }
    >
      {phase === "setup" ? (
        <CredentialsForm mode="setup" onDone={onAuthed} />
      ) : (
        <CredentialsForm mode="login" onDone={onAuthed} />
      )}
    </AuthShell>
  );
}

function AuthShell({
  subtitle,
  children,
}: {
  subtitle: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex min-h-svh items-center justify-center bg-background p-6">
      <div className="w-full max-w-sm">
        <div className="mb-6 flex flex-col items-center text-center">
          <div className="mb-4 grid size-12 place-items-center rounded-xl bg-gradient-to-br from-primary to-primary/60 text-primary-foreground shadow-sm">
            <Snail className="size-6" />
          </div>
          <h1 className="text-lg font-semibold tracking-tight">Transpondarr</h1>
          <p className="mt-1 text-sm text-muted-foreground">{subtitle}</p>
        </div>
        {children}
      </div>
    </div>
  );
}

function CredentialsForm({
  mode,
  onDone,
}: {
  mode: "setup" | "login";
  onDone: () => void | Promise<void>;
}) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const isSetup = mode === "setup";

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (isSetup && password !== confirm) {
      setError("Passwords do not match.");
      return;
    }
    setBusy(true);
    try {
      if (isSetup) await api.setup(username.trim(), password);
      else await api.login(username.trim(), password);
      await onDone();
    } catch (err) {
      if (err instanceof UnauthorizedError) {
        setError("Invalid username or password.");
      } else {
        setError(err instanceof Error ? err.message : "Something went wrong.");
      }
      setBusy(false);
    }
  }

  return (
    <form
      onSubmit={submit}
      className="space-y-3 rounded-lg border bg-card p-4 shadow-sm"
    >
      <label className="block">
        <span className="mb-1 flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
          <User className="size-3.5" /> Username
        </span>
        <Input
          autoFocus
          autoComplete="username"
          value={username}
          onChange={(e) => setUsername(e.target.value)}
        />
      </label>
      <label className="block">
        <span className="mb-1 flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
          <KeyRound className="size-3.5" /> Password
        </span>
        <Input
          type="password"
          autoComplete={isSetup ? "new-password" : "current-password"}
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
      </label>
      {isSetup && (
        <label className="block">
          <span className="mb-1 block text-xs font-medium text-muted-foreground">
            Confirm password
          </span>
          <Input
            type="password"
            autoComplete="new-password"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
          />
        </label>
      )}
      {error && <p className="text-xs text-destructive">{error}</p>}
      <Button
        type="submit"
        className="w-full"
        disabled={busy || !username.trim() || !password}
      >
        {busy && <Loader2 className="size-4 animate-spin" />}
        {isSetup ? "Create account" : "Sign in"}
      </Button>
    </form>
  );
}
