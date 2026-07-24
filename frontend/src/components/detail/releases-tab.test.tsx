import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ScoreBreakdown, ScoreCell } from "@/components/detail/releases-tab";
import type { CandidateRelease } from "@/lib/api";

function release(overrides: Partial<CandidateRelease>): CandidateRelease {
  return {
    title: "[TrustedCorp] Example Show - 03 (1080p)",
    download_url: "magnet:?xt=urn:btih:aaaa",
    size: 700_000_000,
    seeders: 12,
    dual_audio: false,
    matched: true,
    reason: "episode matches a wanted item",
    score: 1400,
    eligible: true,
    score_parts: [
      { label: "group TrustedCorp (rank 1)", points: 1000 },
      { label: "resolution 1080p (rank 1)", points: 400 },
    ],
    ...overrides,
  };
}

describe("ScoreCell", () => {
  it("shows the score", () => {
    render(<ScoreCell r={release({})} />);
    expect(screen.getByText("1400")).toBeInTheDocument();
  });

  it("marks an ineligible release", () => {
    render(
      <ScoreCell
        r={release({
          eligible: false,
          ineligible_reason: "group BadRipCo is blocked by the profile",
        })}
      />,
    );
    expect(screen.getByLabelText(/ineligible/i)).toBeInTheDocument();
  });
});

describe("ScoreBreakdown", () => {
  it("lists each axis contribution with signed points and the total", () => {
    render(<ScoreBreakdown r={release({})} />);
    expect(screen.getByText(/group TrustedCorp/)).toBeInTheDocument();
    expect(screen.getByText("+1000")).toBeInTheDocument();
    expect(screen.getByText("+400")).toBeInTheDocument();
    expect(screen.getByText("1400")).toBeInTheDocument();
  });

  it("shows the ineligible reason", () => {
    render(
      <ScoreBreakdown
        r={release({
          eligible: false,
          ineligible_reason: "score 0 is below the profile minimum 500",
        })}
      />,
    );
    expect(screen.getByText(/below the profile minimum/)).toBeInTheDocument();
  });

  it("falls back when no profile preference matched", () => {
    render(
      <ScoreBreakdown r={release({ score: 0, score_parts: undefined })} />,
    );
    expect(
      screen.getByText(/no profile preferences matched/i),
    ).toBeInTheDocument();
  });
});
