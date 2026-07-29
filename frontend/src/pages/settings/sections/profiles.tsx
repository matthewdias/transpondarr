import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import {
  DndContext,
  KeyboardSensor,
  PointerSensor,
  closestCenter,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import {
  restrictToParentElement,
  restrictToVerticalAxis,
} from "@dnd-kit/modifiers";
import {
  SortableContext,
  arrayMove,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { GripVertical, ListOrdered, Loader2, Plus, X } from "lucide-react";
import { api, ApiError, type QualityProfile } from "@/lib/api";
import {
  EXCLUDE_AXES,
  fromProfile,
  nextKey,
  toProfileInput,
  type EditorState,
} from "./profile-editor-state";
import { profilesQuery } from "@/lib/queries";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Field, SectionShell } from "../section-shell";

// ── Sortable rows (shared by the group and resolution lists) ─────────────────

function SortableRow({
  id,
  children,
}: {
  id: string;
  children: React.ReactNode;
}) {
  const {
    attributes,
    listeners,
    setNodeRef,
    setActivatorNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({ id });
  return (
    <li
      ref={setNodeRef}
      style={{ transform: CSS.Transform.toString(transform), transition }}
      className={cn(
        "flex items-center gap-2 rounded-md border bg-card px-2 py-1.5",
        isDragging && "z-10 shadow-md",
      )}
    >
      <button
        type="button"
        ref={setActivatorNodeRef}
        className="cursor-grab touch-none rounded p-0.5 text-faint outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50 active:cursor-grabbing"
        aria-label="Reorder"
        {...attributes}
        {...listeners}
      >
        <GripVertical className="size-4" />
      </button>
      {children}
    </li>
  );
}

function SortableList<T extends { key: string }>({
  rows,
  onReorder,
  children,
}: {
  rows: T[];
  onReorder: (rows: T[]) => void;
  children: (row: T, index: number) => React.ReactNode;
}) {
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, {
      coordinateGetter: sortableKeyboardCoordinates,
    }),
  );
  const onDragEnd = (e: DragEndEvent) => {
    const { active, over } = e;
    if (!over || active.id === over.id) return;
    const from = rows.findIndex((r) => r.key === active.id);
    const to = rows.findIndex((r) => r.key === over.id);
    onReorder(arrayMove(rows, from, to));
  };
  return (
    <DndContext
      sensors={sensors}
      collisionDetection={closestCenter}
      modifiers={[restrictToVerticalAxis, restrictToParentElement]}
      onDragEnd={onDragEnd}
    >
      <SortableContext
        items={rows.map((r) => r.key)}
        strategy={verticalListSortingStrategy}
      >
        <ul className="space-y-1.5">{rows.map(children)}</ul>
      </SortableContext>
    </DndContext>
  );
}

// ── The editor sheet ─────────────────────────────────────────────────────────

function LabeledSelect({
  label,
  value,
  onChange,
  options,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  options: { value: string; label: string }[];
}) {
  return (
    <label className="block">
      <span className="mb-1 block text-xs font-medium text-muted-foreground">
        {label}
      </span>
      {/* Radix Select can't represent "", so the no-preference item uses a
          sentinel that maps back to empty. */}
      <Select
        value={value === "" ? "none" : value}
        onValueChange={(v) => onChange(v === "none" ? "" : v)}
      >
        <SelectTrigger className="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="none">No preference</SelectItem>
          {options.map((o) => (
            <SelectItem key={o.value} value={o.value}>
              {o.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </label>
  );
}

function ToggleRow({
  label,
  hint,
  checked,
  onChange,
}: {
  label: string;
  hint?: string;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <label className="flex items-center justify-between gap-3">
      <span>
        <span className="block text-xs font-medium text-muted-foreground">
          {label}
        </span>
        {hint && <span className="block text-[11px] text-faint">{hint}</span>}
      </span>
      <Switch checked={checked} onCheckedChange={onChange} />
    </label>
  );
}

// The exclude list is a closed menu, not free text: an exclude that is not an
// axis value the parser emits would silently never fire (#60).
export function ExcludePicker({
  excludes,
  stale,
  onChange,
  onStaleChange,
}: {
  excludes: string[];
  stale: string[];
  onChange: (v: string[]) => void;
  onStaleChange?: (v: string[]) => void;
}) {
  const toggle = (token: string) =>
    onChange(
      excludes.includes(token)
        ? excludes.filter((t) => t !== token)
        : [...excludes, token],
    );

  return (
    <div>
      <span className="mb-1 block text-xs font-medium text-muted-foreground">
        Never take
      </span>
      <span className="mb-2 block text-[11px] text-faint">
        Attributes are read from the release name, so a release that does not
        label one is not caught. Rank trusted groups and set a minimum score for
        real protection.
      </span>
      <div className="space-y-2">
        {EXCLUDE_AXES.map((a) => (
          <div key={a.axis} className="flex flex-wrap items-center gap-1.5">
            <span className="w-16 shrink-0 text-[11px] text-faint">
              {a.axis}
            </span>
            {a.values.map((v) => (
              <Button
                key={v.token}
                type="button"
                variant="outline"
                size="xs"
                aria-pressed={excludes.includes(v.token)}
                onClick={() => toggle(v.token)}
                className={cn(
                  excludes.includes(v.token) &&
                    "border-dl/40 bg-dl-weak text-dl dark:border-dl/40 dark:bg-dl-weak dark:hover:bg-dl-weak/80",
                )}
              >
                {v.label}
              </Button>
            ))}
          </div>
        ))}
      </div>
      {stale.length > 0 && (
        <div className="mt-2 space-y-1 rounded-md border border-dl/30 px-2.5 py-2">
          <span className="block text-[11px] text-dl">
            These can never match — no release carries them on any axis the
            parser reads.
          </span>
          {stale.map((t) => (
            <div key={t} className="flex items-center gap-2">
              <span className="min-w-0 flex-1 truncate font-mono text-xs">
                {t}
              </span>
              <Button
                variant="ghost"
                size="icon-xs"
                aria-label={`Remove ${t}`}
                onClick={() => onStaleChange?.(stale.filter((x) => x !== t))}
              >
                <X className="size-3" />
              </Button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function ProfileEditor({
  profile,
  profiles,
  onClose,
}: {
  profile: QualityProfile | null; // null = create
  profiles: QualityProfile[];
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const [state, setState] = useState<EditorState>(() => fromProfile(profile));
  const [newGroup, setNewGroup] = useState("");
  const [deleting, setDeleting] = useState(false);
  const set = <K extends keyof EditorState>(k: K, v: EditorState[K]) =>
    setState((s) => ({ ...s, [k]: v }));

  const save = useMutation({
    mutationFn: () =>
      profile
        ? api.updateProfile(profile.id, toProfileInput(state))
        : api.createProfile(toProfileInput(state)),
    onSuccess: (p) => {
      toast.success(`Profile “${p.name}” saved`);
      queryClient.invalidateQueries({ queryKey: profilesQuery().queryKey });
      onClose();
    },
    onError: (e) =>
      toast.error("Save failed", {
        description: e instanceof Error ? e.message : String(e),
      }),
  });

  const addGroup = () => {
    const name = newGroup.trim();
    if (!name) return;
    if (state.groups.some((g) => g.name.toLowerCase() === name.toLowerCase())) {
      toast.error(`“${name}” is already in the list`);
      return;
    }
    set("groups", [...state.groups, { key: nextKey(), name, blocked: false }]);
    setNewGroup("");
  };

  return (
    <div className="flex h-full flex-col">
      <div className="flex-1 space-y-5 overflow-y-auto px-4 pb-4">
        <Field
          label="Name"
          value={state.name}
          onChange={(e) => set("name", e.target.value)}
          placeholder="e.g. Trusted subs"
        />

        <div>
          <span className="mb-1 block text-xs font-medium text-muted-foreground">
            Release groups — most preferred first
          </span>
          <span className="mb-2 block text-[11px] text-faint">
            Group is the dominant signal: any listed group outranks every
            unlisted one. Blocked groups are never taken.
          </span>
          <SortableList
            rows={state.groups}
            onReorder={(rows) => set("groups", rows)}
          >
            {(g, i) => (
              <SortableRow key={g.key} id={g.key}>
                <span
                  className={cn(
                    "w-5 text-right text-[11px] tabular-nums text-faint",
                    g.blocked && "invisible",
                  )}
                >
                  {state.groups.filter((x) => !x.blocked).indexOf(g) + 1}
                </span>
                <span
                  className={cn(
                    "min-w-0 flex-1 truncate text-sm",
                    g.blocked && "text-dl line-through",
                  )}
                >
                  {g.name}
                </span>
                <label className="flex items-center gap-1.5 text-[11px] text-faint">
                  Block
                  <Switch
                    size="sm"
                    checked={g.blocked}
                    onCheckedChange={(v) =>
                      set(
                        "groups",
                        state.groups.map((x, xi) =>
                          xi === i ? { ...x, blocked: v } : x,
                        ),
                      )
                    }
                  />
                </label>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={`Remove ${g.name}`}
                  onClick={() =>
                    set(
                      "groups",
                      state.groups.filter((_, xi) => xi !== i),
                    )
                  }
                >
                  <X className="size-3.5" />
                </Button>
              </SortableRow>
            )}
          </SortableList>
          <div className="mt-2 flex gap-2">
            <Input
              value={newGroup}
              onChange={(e) => setNewGroup(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  addGroup();
                }
              }}
              placeholder="Add a release group…"
            />
            <Button variant="outline" onClick={addGroup}>
              <Plus className="size-4" /> Add
            </Button>
          </div>
        </div>

        <div>
          <span className="mb-1 block text-xs font-medium text-muted-foreground">
            Resolutions — best first
          </span>
          <span className="mb-2 block text-[11px] text-faint">
            Excluded resolutions score zero but stay grabbable.
          </span>
          <SortableList
            rows={state.resolutions}
            onReorder={(rows) => set("resolutions", rows)}
          >
            {(r, i) => (
              <SortableRow key={r.key} id={r.key}>
                <span
                  className={cn(
                    "min-w-0 flex-1 text-sm",
                    !r.included && "text-faint",
                  )}
                >
                  {r.name}
                </span>
                <Switch
                  size="sm"
                  checked={r.included}
                  aria-label={`Include ${r.name}`}
                  onCheckedChange={(v) =>
                    set(
                      "resolutions",
                      state.resolutions.map((x, xi) =>
                        xi === i ? { ...x, included: v } : x,
                      ),
                    )
                  }
                />
              </SortableRow>
            )}
          </SortableList>
        </div>

        <div className="grid gap-3.5 sm:grid-cols-2">
          <LabeledSelect
            label="Preferred source"
            value={state.preferredSource}
            onChange={(v) => set("preferredSource", v)}
            options={[
              { value: "web", label: "WEB" },
              { value: "bd", label: "Blu-ray (BD)" },
              { value: "tv", label: "TV" },
              { value: "dvd", label: "DVD" },
            ]}
          />
          <LabeledSelect
            label="Preferred codec"
            value={state.codecPref}
            onChange={(v) => set("codecPref", v)}
            options={[
              { value: "h264", label: "H.264" },
              { value: "h265", label: "H.265 / HEVC" },
              { value: "av1", label: "AV1" },
            ]}
          />
          <LabeledSelect
            label="Subtitles"
            value={state.subPref}
            onChange={(v) => set("subPref", v)}
            options={[
              { value: "softsub", label: "Softsub" },
              { value: "hardsub", label: "Hardsub" },
            ]}
          />
          <Field
            label="Minimum score"
            type="number"
            min={0}
            value={state.minScore}
            onChange={(e) =>
              set("minScore", Math.max(0, Number(e.target.value) || 0))
            }
            hint="Releases scoring below are ineligible — the answer can be “nothing yet”."
          />
        </div>

        <div className="space-y-3 rounded-md border bg-panel-2/40 px-3 py-3">
          <ToggleRow
            label="Prefer dual audio"
            checked={state.dualAudio}
            onChange={(v) => set("dualAudio", v)}
          />
          <ExcludePicker
            excludes={state.excludes}
            stale={state.staleExcludes}
            onChange={(v) => set("excludes", v)}
            onStaleChange={(v) => set("staleExcludes", v)}
          />
        </div>
      </div>

      <div className="flex items-center gap-2 border-t bg-panel-2/40 px-4 py-3">
        <Button
          size="sm"
          onClick={() => save.mutate()}
          disabled={save.isPending || !state.name.trim()}
        >
          {save.isPending && <Loader2 className="size-4 animate-spin" />}
          Save
        </Button>
        <Button variant="outline" size="sm" onClick={onClose}>
          Cancel
        </Button>
        <span className="flex-1" />
        {profile && !profile.is_default && (
          <Button
            variant="outline"
            size="sm"
            className="text-destructive"
            onClick={() => setDeleting(true)}
          >
            Delete
          </Button>
        )}
      </div>

      {profile && (
        <DeleteProfileDialog
          open={deleting}
          profile={profile}
          profiles={profiles}
          onOpenChange={setDeleting}
          onDeleted={onClose}
        />
      )}
    </div>
  );
}

// The prompt-to-migrate delete flow: a profile in use is never deleted from
// under its series — the user picks where those series go first.
function DeleteProfileDialog({
  open,
  profile,
  profiles,
  onOpenChange,
  onDeleted,
}: {
  open: boolean;
  profile: QualityProfile;
  profiles: QualityProfile[];
  onOpenChange: (o: boolean) => void;
  onDeleted: () => void;
}) {
  const queryClient = useQueryClient();
  const targets = profiles.filter((p) => p.id !== profile.id);
  const [target, setTarget] = useState<number>(
    () => targets.find((p) => p.is_default)?.id ?? targets[0]?.id ?? 0,
  );
  const inUse = profile.series_count > 0;

  const del = useMutation({
    mutationFn: () => api.deleteProfile(profile.id, inUse ? target : undefined),
    onSuccess: () => {
      toast.success(`Profile “${profile.name}” deleted`);
      queryClient.invalidateQueries({ queryKey: profilesQuery().queryKey });
      queryClient.invalidateQueries({ queryKey: ["series"] });
      onOpenChange(false);
      onDeleted();
    },
    onError: (e) =>
      toast.error("Delete failed", {
        description: e instanceof ApiError ? e.message : String(e),
      }),
  });

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete “{profile.name}”?</DialogTitle>
          <DialogDescription>
            {inUse
              ? `${profile.series_count} series ${profile.series_count === 1 ? "uses" : "use"} this profile. Pick the profile they move to.`
              : "No series use this profile."}
          </DialogDescription>
        </DialogHeader>
        {inUse && (
          <Select
            value={String(target)}
            onValueChange={(v) => setTarget(Number(v))}
          >
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {targets.map((p) => (
                <SelectItem key={p.id} value={String(p.id)}>
                  {p.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            variant="destructive"
            onClick={() => del.mutate()}
            disabled={del.isPending || (inUse && !target)}
          >
            {del.isPending && <Loader2 className="size-4 animate-spin" />}
            {inUse ? "Move series and delete" : "Delete"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ── The settings section ─────────────────────────────────────────────────────

export function ProfilesSection() {
  const profiles = useQuery(profilesQuery());
  // undefined = closed; null = creating; profile = editing
  const [editing, setEditing] = useState<QualityProfile | null | undefined>(
    undefined,
  );
  const list = useMemo(() => profiles.data ?? [], [profiles.data]);

  return (
    <SectionShell
      icon={ListOrdered}
      title="Quality profiles"
      description="What a release should be — ranked groups first, then resolution, source, subs, codec."
    >
      {profiles.isLoading && (
        <div className="flex items-center gap-2 py-2 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" /> Loading profiles…
        </div>
      )}
      {profiles.isError && (
        <p className="text-sm text-destructive">
          Failed to load profiles:{" "}
          {profiles.error instanceof Error
            ? profiles.error.message
            : String(profiles.error)}
        </p>
      )}
      {list.map((p) => (
        <button
          key={p.id}
          type="button"
          onClick={() => setEditing(p)}
          className="flex w-full items-center gap-3 rounded-md border bg-card px-3 py-2.5 text-left transition-colors hover:bg-panel-2/60 focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none"
        >
          <span className="min-w-0 flex-1">
            <span className="flex items-center gap-2">
              <span className="text-sm font-medium">{p.name}</span>
              {p.is_default && (
                <span className="rounded-full border border-transparent bg-panel-2 px-2 py-0.5 text-[11px] font-medium text-faint">
                  Default
                </span>
              )}
            </span>
            <span className="mt-0.5 block text-xs text-muted-foreground">
              {p.groups.filter((g) => !g.blocked).length} ranked ·{" "}
              {p.groups.filter((g) => g.blocked).length} blocked ·{" "}
              {p.series_count} series
            </span>
          </span>
          <span className="text-xs text-faint">Edit</span>
        </button>
      ))}
      <Button variant="outline" size="sm" onClick={() => setEditing(null)}>
        <Plus className="size-4" /> New profile
      </Button>

      <Sheet
        open={editing !== undefined}
        onOpenChange={(o) => !o && setEditing(undefined)}
      >
        <SheetContent className="w-full gap-0 overflow-hidden sm:max-w-md">
          <SheetHeader>
            <SheetTitle>
              {editing ? `Edit “${editing.name}”` : "New profile"}
            </SheetTitle>
            <SheetDescription>
              Scoring ranks releases for every series on this profile.
            </SheetDescription>
          </SheetHeader>
          {editing !== undefined && (
            <ProfileEditor
              key={editing?.id ?? "new"}
              profile={editing}
              profiles={list}
              onClose={() => setEditing(undefined)}
            />
          )}
        </SheetContent>
      </Sheet>
    </SectionShell>
  );
}
