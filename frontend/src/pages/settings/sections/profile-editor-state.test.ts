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

  it("serializes a mid-list blocked row after the unblocked ones", () => {
    const state = fromProfile(profile({}));
    const midBlocked: EditorState = {
      ...state,
      groups: state.groups.map((g) =>
        g.name === "FirstChoice" ? { ...g, blocked: true } : g,
      ),
    };
    // The store reads blocked rows last, so serializing them anywhere else
    // would make save-then-reopen reshuffle the list.
    expect(toProfileInput(midBlocked).groups).toEqual([
      { name: "SecondChoice", blocked: false },
      { name: "FirstChoice", blocked: true },
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

  it("reads every axis value the parser can emit as a selectable exclude", () => {
    const state = fromProfile(
      profile({ hard_excludes: ["hardsub", "480p", "h265", "dvd"] }),
    );
    expect(state.excludes).toEqual(["hardsub", "h265", "dvd", "480p"]);
    expect(state.staleExcludes).toEqual([]);
  });

  it("canonicalizes stored excludes that differ only in case", () => {
    const state = fromProfile(profile({ hard_excludes: ["HardSub", "H265"] }));
    expect(state.excludes).toEqual(["hardsub", "h265"]);
    expect(toProfileInput(state).hard_excludes).toEqual(["hardsub", "h265"]);
  });

  it("treats a resolution outside the offered list as matchable, not stale", () => {
    const state = fromProfile(profile({ hard_excludes: ["540p"] }));
    expect(state.excludes).toEqual(["540p"]);
    expect(state.staleExcludes).toEqual([]);
  });

  it("treats anitogo's dimension form as matchable — it is emitted verbatim", () => {
    const state = fromProfile(profile({ hard_excludes: ["1920x1080"] }));
    expect(state.excludes).toEqual(["1920x1080"]);
    expect(state.staleExcludes).toEqual([]);
  });

  it("separates stored tokens no release can ever carry, without dropping them", () => {
    const state = fromProfile(profile({ hard_excludes: ["dub", "hardsub"] }));
    expect(state.excludes).toEqual(["hardsub"]);
    expect(state.staleExcludes).toEqual(["dub"]);
    expect(toProfileInput(state).hard_excludes).toEqual(["hardsub", "dub"]);
  });

  it("surfaces excluded resolutions as unincluded rows that do not serialize", () => {
    const state = fromProfile(profile({ resolution_order: ["720p"] }));
    const names = state.resolutions.map((r) => `${r.name}:${r.included}`);
    expect(names).toEqual([
      "2160p:false",
      "1440p:false",
      "1080p:false",
      "720p:true",
      "480p:false",
    ]);
    expect(toProfileInput(state).resolution_order).toEqual(["720p"]);
  });

  it("offers 4K above 1080p but leaves it off, so scoring is unchanged", () => {
    const state = fromProfile(null);
    expect(state.resolutions.map((r) => `${r.name}:${r.included}`)).toEqual([
      "2160p:false",
      "1440p:false",
      "1080p:true",
      "720p:true",
      "480p:true",
    ]);
    expect(toProfileInput(state).resolution_order).toEqual([
      "1080p",
      "720p",
      "480p",
    ]);
    expect(state.name).toBe("");
    expect(state.excludes).toEqual([]);
  });

  it("ranks 4K first once it is switched on, without a drag", () => {
    const state = fromProfile(null);
    const on: EditorState = {
      ...state,
      resolutions: state.resolutions.map((r) =>
        r.name === "2160p" ? { ...r, included: true } : r,
      ),
    };
    expect(toProfileInput(on).resolution_order).toEqual([
      "2160p",
      "1080p",
      "720p",
      "480p",
    ]);
  });
});
