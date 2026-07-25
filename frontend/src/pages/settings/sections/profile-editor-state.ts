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
  neverHardsub: boolean;
  neverH265: boolean;
  otherExcludes: string[]; // tokens this UI has no toggle for — preserved, never dropped
  minScore: number;
};

const RESOLUTIONS = ["1080p", "720p", "480p"];

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
  const excludes = p?.hard_excludes ?? [];
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
    neverHardsub: excludes.includes("hardsub"),
    neverH265: excludes.includes("h265"),
    otherExcludes: excludes.filter((t) => t !== "hardsub" && t !== "h265"),
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
    hard_excludes: [
      ...s.otherExcludes,
      ...(s.neverHardsub ? ["hardsub"] : []),
      ...(s.neverH265 ? ["h265"] : []),
    ],
    min_score: s.minScore,
    groups: s.groups.map((g) => ({ name: g.name, blocked: g.blocked })),
  };
}
