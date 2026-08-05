// Mirrors the score weights in internal/core/decide/decide.go, so an upgrade
// cutoff is chosen as a landmark ("top group, best resolution") rather than as a
// bare number nobody can rank. A Go guard test pins the two the ladder is built
// from (TestScoreLandmarksArePinned), so a reweighting has to move this file too.
const GROUP_BASE = 2000;
const GROUP_MIN = 1000;
const RES_BASE = 400;
const RES_STEP = 100;

export type ScoreLandmark = { score: number; label: string };

// Worst first, so the select reads as a ladder of "good enough".
export const SCORE_LANDMARKS: ScoreLandmark[] = [
  { score: RES_BASE, label: "Best resolution, any group" },
  { score: GROUP_MIN, label: "Any ranked group" },
  { score: GROUP_MIN + RES_BASE, label: "Any ranked group, best resolution" },
  { score: GROUP_BASE, label: "Top-ranked group" },
  {
    score: GROUP_BASE + RES_BASE - RES_STEP,
    label: "Top group, second-best resolution",
  },
  { score: GROUP_BASE + RES_BASE, label: "Top group, best resolution" },
];

// What a first opt-in lands on: upgrade until the held release is the best this
// profile describes.
export const DEFAULT_CUTOFF = GROUP_BASE + RES_BASE;

// A stored cutoff matching no landmark is named, never rewritten — the same
// stance stale excludes take.
export function landmarkLabel(score: number): string {
  return (
    SCORE_LANDMARKS.find((l) => l.score === score)?.label ?? `Custom (${score})`
  );
}
