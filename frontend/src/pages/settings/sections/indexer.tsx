import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Rss } from "lucide-react";
import { api, type Settings, type IndexerInput } from "@/lib/api";
import {
  Field,
  SectionShell,
  SectionFooter,
  type TestState,
} from "../section-shell";
import { useSaveToast } from "../section-helpers";

export function IndexerSection({ settings }: { settings: Settings }) {
  const i = settings.indexer;
  const queryClient = useQueryClient();
  const [name, setName] = useState(i.name);
  const [url, setUrl] = useState(i.url);
  const [apikey, setApikey] = useState("");
  const [categories, setCategories] = useState(i.categories);
  const [testState, setTestState] = useState<TestState>(null);

  const body = (): IndexerInput => ({
    name,
    url,
    apikey: apikey || undefined,
    categories,
  });

  const test = useMutation({
    mutationFn: () => api.testIndexer(body()),
    onSuccess: () => setTestState({ ok: true, message: "Indexer responded." }),
    onError: (e) =>
      setTestState({
        ok: false,
        message: e instanceof Error ? e.message : String(e),
      }),
  });
  const save = useMutation({
    mutationFn: () => api.updateIndexer(body()),
    ...useSaveToast(queryClient, "Indexer"),
  });

  return (
    <SectionShell
      icon={Rss}
      title="Indexer"
      description="Torznab feed (Prowlarr or Jackett) used to find releases."
      configured={i.configured}
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
        label="Name"
        value={name}
        onChange={(e) => setName(e.target.value)}
      />
      <Field
        label="Torznab URL"
        placeholder="http://prowlarr:9696/…/api"
        value={url}
        onChange={(e) => setUrl(e.target.value)}
        hint="A Prowlarr aggregate feed already fans out across trackers."
      />
      <Field
        label="Categories"
        placeholder="5070"
        value={categories}
        onChange={(e) => setCategories(e.target.value)}
        hint="Comma-separated Newznab category IDs narrowing every search and the recent feed (anime is usually 5070). Leave empty for no filter."
      />
      <Field
        label="API key"
        type="password"
        value={apikey}
        onChange={(e) => setApikey(e.target.value)}
        placeholder={i.apikey_set ? "•••••••• (unchanged)" : ""}
        hint={i.apikey_set ? "Leave blank to keep the stored key." : undefined}
        autoComplete="off"
      />
    </SectionShell>
  );
}
