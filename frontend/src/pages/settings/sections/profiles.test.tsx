import { describe, expect, it } from "vitest";
import {
  fromProfile,
  toProfileInput,
  type EditorState,
} from "@/pages/settings/sections/profile-editor-state";
import type { QualityProfile } from "@/lib/api";

function profile(overrides: Partial<QualityProfile>): QualityProfile {
  return {
    id: 2,
    name: "Trusted",
    is_default: false,
    resolution_order: ["1080p", "720p"],
    preferred_source: "web",
    sub_pref: "softsub",
    prefer_dual_audio: true,
    codec_pref: "h265",
    hard_excludes: ["hardsub"],
    min_score: 100,
    groups: [
      { name: "FirstChoice" },
      { name: "SecondChoice" },
      { name: "BadRipCo", blocked: true },
    ],
    series_count: 0,
    ...overrides,
  };
}

describe("fromProfile / toProfileInput", () => {
  it("round-trips a profile through the editor state", () => {
    const input = toProfileInput(fromProfile(profile({})));
    expect(input.name).toBe("Trusted");
    expect(input.resolution_order).toEqual(["1080p", "720p"]);
    expect(input.hard_excludes).toEqual(["hardsub"]);
    expect(input.groups).toEqual([
      { name: "FirstChoice", blocked: false },
      { name: "SecondChoice", blocked: false },
      { name: "BadRipCo", blocked: true },
    ]);
  });

  it("keeps group array order as the rank", () => {
    const state = fromProfile(profile({}));
    const swapped: EditorState = {
      ...state,
      groups: [state.groups[1], state.groups[0], state.groups[2]],
    };
    expect(toProfileInput(swapped).groups?.map((g) => g.name)).toEqual([
      "SecondChoice",
      "FirstChoice",
      "BadRipCo",
    ]);
  });

  it("maps the never-take toggles onto hard_excludes without dropping unknown tokens", () => {
    const state = fromProfile(
      profile({ hard_excludes: ["hardsub", "480p", "h265"] }),
    );
    expect(state.neverHardsub).toBe(true);
    expect(state.neverH265).toBe(true);
    expect(state.otherExcludes).toEqual(["480p"]);

    const off = { ...state, neverHardsub: false };
    expect(toProfileInput(off).hard_excludes).toEqual(["480p", "h265"]);
  });

  it("surfaces excluded resolutions as unincluded rows that do not serialize", () => {
    const state = fromProfile(profile({ resolution_order: ["720p"] }));
    const names = state.resolutions.map((r) => `${r.name}:${r.included}`);
    expect(names).toEqual(["720p:true", "1080p:false", "480p:false"]);
    expect(toProfileInput(state).resolution_order).toEqual(["720p"]);
  });

  it("starts a new profile with every resolution included", () => {
    const state = fromProfile(null);
    expect(toProfileInput(state).resolution_order).toEqual([
      "1080p",
      "720p",
      "480p",
    ]);
    expect(state.name).toBe("");
  });
});
