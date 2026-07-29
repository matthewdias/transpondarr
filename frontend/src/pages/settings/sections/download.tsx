import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { HardDriveDownload } from "lucide-react";
import { api, type Settings, type DownloadInput } from "@/lib/api";
import {
  Field,
  SectionShell,
  SectionFooter,
  type TestState,
} from "../section-shell";
import { useSaveToast } from "../section-helpers";

export function DownloadSection({ settings }: { settings: Settings }) {
  const d = settings.download;
  const queryClient = useQueryClient();
  const [url, setUrl] = useState(d.url);
  const [user, setUser] = useState(d.user);
  const [password, setPassword] = useState("");
  const [category, setCategory] = useState(d.category);
  const [testState, setTestState] = useState<TestState>(null);

  const body = (): DownloadInput => ({
    url,
    user,
    password: password || undefined,
    category,
  });

  const test = useMutation({
    mutationFn: () => api.testDownload(body()),
    onSuccess: () =>
      setTestState({ ok: true, message: "Connected to qBittorrent." }),
    onError: (e) =>
      setTestState({
        ok: false,
        message: e instanceof Error ? e.message : String(e),
      }),
  });
  const save = useMutation({
    mutationFn: () => api.updateDownload(body()),
    ...useSaveToast(queryClient, "Download client"),
  });

  return (
    <SectionShell
      icon={HardDriveDownload}
      title="Download client"
      description="qBittorrent — where grabbed torrents are sent."
      configured={d.configured}
      footer={
        <SectionFooter
          onTest={() => test.mutate()}
          onSave={() => save.mutate()}
          testing={test.isPending}
          saving={save.isPending}
          testState={testState}
        />
      }
    >
      <Field
        label="WebUI URL"
        placeholder="http://localhost:8080"
        value={url}
        onChange={(e) => setUrl(e.target.value)}
      />
      <div className="grid gap-3.5 sm:grid-cols-2">
        <Field
          label="Username"
          value={user}
          onChange={(e) => setUser(e.target.value)}
          autoComplete="off"
        />
        <Field
          label="Password"
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          placeholder={d.password_set ? "•••••••• (unchanged)" : ""}
          hint={
            d.password_set
              ? "Leave blank to keep the stored password."
              : undefined
          }
          autoComplete="new-password"
        />
      </div>
      <Field
        label="Category"
        value={category}
        onChange={(e) => setCategory(e.target.value)}
        hint="Applied to grabbed torrents so they're identifiable in the client."
      />
    </SectionShell>
  );
}
