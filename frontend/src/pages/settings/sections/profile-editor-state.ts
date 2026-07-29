import type { ProfileInput, QualityProfile } from "@/lib/api";

export type GroupRow = { key: string; name: string; blocked: boolean };
export type ResolutionRow = { key: string; name: string; included: boolean };

export type EditorState = {
  name: string;
  groups: GroupRow[];
  resolutions: ResolutionRow[];
  preferredSource: string;
  subPref: string;
  dualAudio: boolean;
  codecPref: string;
  excludes: string[];
  staleExcludes: string[]; // stored tokens no release can carry — surfaced, never dropped
  minScore: number;
};

const RESOLUTIONS = ["1080p", "720p", "480p"];

export type ExcludeValue = { token: string; label: string };

// What the parser normalizes each axis to, and so the only tokens an exclude
// can ever match — anything else would silently never fire.
export const EXCLUDE_AXES: { axis: string; values: ExcludeValue[] }[] = [
  {
    axis: "Subtitles",
    values: [
      { token: "hardsub", label: "Hardsub" },
      { token: "softsub", label: "Softsub" },
    ],
  },
  {
    axis: "Codec",
    values: [
      { token: "h264", label: "H.264" },
      { token: "h265", label: "H.265 / HEVC" },
      { token: "av1", label: "AV1" },
    ],
  },
  {
    axis: "Source",
    values: [
      { token: "web", label: "WEB" },
      { token: "bd", label: "Blu-ray (BD)" },
      { token: "tv", label: "TV" },
      { token: "dvd", label: "DVD" },
    ],
  },
  {
    axis: "Resolution",
    values: RESOLUTIONS.map((r) => ({ token: r, label: r })),
  },
];

const EXCLUDE_TOKENS = EXCLUDE_AXES.flatMap((a) =>
  a.values.map((v) => v.token),
);

// Resolution is passed through from the parser rather than normalized, so a
// height this UI does not offer (2160p) still matches and must not be called stale.
const resolutionLike = /^\d{3,4}p$/;

function canonicalExclude(token: string): string | null {
  const t = token.trim().toLowerCase();
  const known = EXCLUDE_TOKENS.find((v) => v === t);
  if (known) return known;
  return resolutionLike.test(t) ? t : null;
}

let rowKey = 0;
export const nextKey = () => `row-${rowKey++}`;

export function fromProfile(p: QualityProfile | null): EditorState {
  const order = p?.resolution_order ?? RESOLUTIONS;
  const resolutions: ResolutionRow[] = [
    ...order.map((name) => ({ key: nextKey(), name, included: true })),
    ...RESOLUTIONS.filter((r) => !order.includes(r)).map((name) => ({
      key: nextKey(),
      name,
      included: false,
    })),
  ];
  const stored = (p?.hard_excludes ?? []).map((t) => ({
    raw: t,
    canonical: canonicalExclude(t),
  }));
  const matchable = stored.flatMap((s) => (s.canonical ? [s.canonical] : []));
  return {
    name: p?.name ?? "",
    groups: (p?.groups ?? []).map((g) => ({
      key: nextKey(),
      name: g.name,
      blocked: g.blocked ?? false,
    })),
    resolutions,
    preferredSource: p?.preferred_source ?? "",
    subPref: p?.sub_pref ?? "",
    dualAudio: p?.prefer_dual_audio ?? false,
    codecPref: p?.codec_pref ?? "",
    excludes: [
      ...EXCLUDE_TOKENS.filter((t) => matchable.includes(t)),
      ...matchable.filter((t) => !EXCLUDE_TOKENS.includes(t)),
    ],
    staleExcludes: stored.flatMap((s) => (s.canonical ? [] : [s.raw])),
    minScore: p?.min_score ?? 0,
  };
}

export function toProfileInput(s: EditorState): ProfileInput {
  return {
    name: s.name.trim(),
    resolution_order: s.resolutions
      .filter((r) => r.included)
      .map((r) => r.name),
    preferred_source: s.preferredSource,
    sub_pref: s.subPref,
    prefer_dual_audio: s.dualAudio,
    codec_pref: s.codecPref,
    hard_excludes: [...s.excludes, ...s.staleExcludes],
    min_score: s.minScore,
    groups: s.groups.map((g) => ({ name: g.name, blocked: g.blocked })),
  };
}
