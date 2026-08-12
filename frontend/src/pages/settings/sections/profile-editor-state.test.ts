import { describe, expect, it } from "vitest";
import {
  fromProfile,
  setGroupBlocked,
  toProfileInput,
  type EditorState,
} from "@/pages/settings/sections/profile-editor-state";
import {
  DEFAULT_CUTOFF,
  SCORE_LANDMARKS,
  landmarkLabel,
} from "@/lib/score-landmarks";
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
    title_count: 0,
    upgrades_enabled: false,
    cutoff_score: 0,
    upgrade_v2_above_cutoff: true,
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

  // Blocking must not yank the row away from the cursor mid-edit; blocked-last
  // is a serialization concern (the test above), not a live-list one.
  it("keeps a newly blocked row under the cursor", () => {
    const state = fromProfile(profile({}));
    const rows = setGroupBlocked(state.groups, 0, true);
    expect(rows.map((g) => `${g.name}:${g.blocked}`)).toEqual([
      "FirstChoice:true",
      "SecondChoice:false",
      "BadRipCo:true",
    ]);
  });

  it("keeps an unblocked row in place too", () => {
    const state = fromProfile(profile({}));
    const rows = setGroupBlocked(state.groups, 2, false);
    expect(rows.map((g) => `${g.name}:${g.blocked}`)).toEqual([
      "FirstChoice:false",
      "SecondChoice:false",
      "BadRipCo:false",
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

  it("treats a stored dimension form as stale — the parser now folds it away", () => {
    const state = fromProfile(profile({ hard_excludes: ["1920x1080"] }));
    expect(state.excludes).toEqual([]);
    expect(state.staleExcludes).toEqual(["1920x1080"]);
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
      "576p:false",
      "480p:false",
      "360p:false",
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
      "576p:false",
      "480p:true",
      "360p:false",
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

describe("upgrade policy", () => {
  it("round-trips the policy a profile stores", () => {
    const input = toProfileInput(
      fromProfile(
        profile({
          upgrades_enabled: true,
          cutoff_score: 2400,
          upgrade_v2_above_cutoff: false,
        }),
      ),
    );
    expect(input.upgrades_enabled).toBe(true);
    expect(input.cutoff_score).toBe(2400);
    expect(input.upgrade_v2_above_cutoff).toBe(false);
  });

  it("starts a new profile opted out, with the carve-out ready", () => {
    const state = fromProfile(null);
    expect(state.upgradesEnabled).toBe(false);
    expect(state.cutoffScore).toBe(0);
    // Inert while upgrades are off, and the default a user wants once they are on.
    expect(state.upgradeV2AboveCutoff).toBe(true);
  });

  it("keeps a stored cutoff that matches no landmark", () => {
    const state = fromProfile(
      profile({ upgrades_enabled: true, cutoff_score: 1234 }),
    );
    expect(state.cutoffScore).toBe(1234);
    expect(toProfileInput(state).cutoff_score).toBe(1234);
    expect(landmarkLabel(1234)).toBe("Custom (1234)");
  });

  it("names the landmarks the score weights compose", () => {
    expect(landmarkLabel(2400)).toBe("Top group, best resolution");
    expect(landmarkLabel(2000)).toBe("Top-ranked group");
    expect(SCORE_LANDMARKS.map((l) => l.score)).toEqual([
      400, 1000, 1400, 2000, 2300, 2400,
    ]);
    // The default a first opt-in lands on is the strictest landmark, so
    // "upgrade until" means "until it is the best this profile describes".
    expect(DEFAULT_CUTOFF).toBe(2400);
  });
});
