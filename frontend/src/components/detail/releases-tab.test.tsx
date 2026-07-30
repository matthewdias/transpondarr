import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import {
  GroupCell,
  ScoreBreakdown,
  ScoreCell,
} from "@/components/detail/releases-tab";
import { grabToast } from "@/components/detail/grab-toast";
import { TooltipProvider } from "@/components/ui/tooltip";
import type { CandidateRelease, GrabResult } from "@/lib/api";

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
    pinned: false,
    score_parts: [
      { label: "group TrustedCorp (rank 1)", points: 1000 },
      { label: "resolution 1080p (rank 1)", points: 400 },
    ],
    ...overrides,
  };
}

function renderCell(r: CandidateRelease) {
  return render(
    <TooltipProvider>
      <ScoreCell r={r} />
    </TooltipProvider>,
  );
}

function grabResult(overrides: Partial<GrabResult>): GrabResult {
  return {
    release: "[TrustedCorp] Example Show - 03 (1080p)",
    infohash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    items: [3],
    outcome: "success",
    ...overrides,
  };
}

describe("ScoreCell", () => {
  it("shows the score", () => {
    renderCell(release({}));
    expect(screen.getByText("1400")).toBeInTheDocument();
  });

  it("marks an ineligible release", () => {
    renderCell(
      release({
        eligible: false,
        ineligible_reason: "group BadRipCo is blocked by the profile",
      }),
    );
    expect(screen.getByLabelText(/ineligible/i)).toBeInTheDocument();
  });

  // The breakdown hangs off a tooltip, so it has to open without a mouse —
  // the drawer is the mobile path, not the keyboard one.
  it("opens the breakdown on keyboard focus", async () => {
    renderCell(release({}));
    expect(screen.queryByText(/group TrustedCorp/)).not.toBeInTheDocument();
    await userEvent.tab();
    expect(screen.getByText(/group TrustedCorp/)).toBeInTheDocument();
  });

  // The default tooltip surface is inverted, where the palette's semantic
  // tokens fail contrast — text-dl drops to 1.84:1 in dark mode. The breakdown
  // needs the same upright surface the drawer gives it.
  it("renders the breakdown on an upright surface", async () => {
    renderCell(release({}));
    await userEvent.tab();
    const content = document.querySelector('[data-slot="tooltip-content"]');
    expect(content?.className).toMatch(/bg-popover/);
    expect(content?.className).not.toMatch(/bg-foreground/);
  });
});

describe("GroupCell", () => {
  it("marks only the pinned group with a pin", () => {
    const { rerender } = render(
      <GroupCell r={release({ release_group: "ShinyRip", pinned: true })} />,
    );
    expect(screen.getByText("ShinyRip")).toBeInTheDocument();
    expect(screen.getByLabelText(/pinned/i)).toBeInTheDocument();
    rerender(<GroupCell r={release({ release_group: "TrustedCorp" })} />);
    expect(screen.queryByLabelText(/pinned/i)).not.toBeInTheDocument();
  });

  it("falls back to a dash when the group is unknown", () => {
    render(<GroupCell r={release({})} />);
    expect(screen.getByText("—")).toBeInTheDocument();
  });

  // The marker explains a ranking outcome, and an unmatched release has none —
  // a foreign-title reject still parses a group, so it would otherwise be
  // marked on a row that has nothing to do with this series.
  it("withholds the pin from an unmatched release", () => {
    render(
      <GroupCell
        r={release({
          release_group: "ShinyRip",
          pinned: true,
          matched: false,
          reason: "release is for a different series",
        })}
      />,
    );
    expect(screen.getByText("ShinyRip")).toBeInTheDocument();
    expect(screen.queryByLabelText(/pinned/i)).not.toBeInTheDocument();
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

  it("caveats the reason as name-based so an exclude reads as no guarantee", () => {
    render(
      <ScoreBreakdown
        r={release({
          eligible: false,
          ineligible_reason: "release is hardsub (excluded by the profile)",
        })}
      />,
    );
    expect(screen.getByText(/read from the release name/i)).toBeInTheDocument();
  });

  it("keeps the caveat off an eligible release", () => {
    render(<ScoreBreakdown r={release({})} />);
    expect(
      screen.queryByText(/read from the release name/i),
    ).not.toBeInTheDocument();
  });

  it("falls back when no profile preference matched", () => {
    render(
      <ScoreBreakdown r={release({ score: 0, score_parts: undefined })} />,
    );
    expect(
      screen.getByText(/no profile preferences matched/i),
    ).toBeInTheDocument();
  });

  it("keeps the profile's amber on the reason", () => {
    render(
      <ScoreBreakdown
        r={release({
          eligible: false,
          ineligible_reason: "group BadRipCo is blocked by the profile",
        })}
      />,
    );
    expect(screen.getByText(/blocked by the profile/).className).toMatch(
      /text-dl/,
    );
  });
});

describe("grabToast", () => {
  it("reports a plain grab with the release and outcome", () => {
    const t = grabToast(grabResult({}));
    expect(t.level).toBe("success");
    expect(t.description).toContain("[TrustedCorp] Example Show - 03 (1080p)");
    expect(t.description).toContain("success");
  });

  it("keeps the release alongside the profile's reason", () => {
    const t = grabToast(
      grabResult({
        ineligible_reason: "group BadRipCo is blocked by the profile",
      }),
    );
    expect(t.level).toBe("warning");
    expect(t.description).toContain("[TrustedCorp] Example Show - 03 (1080p)");
    expect(t.description).toContain("blocked by the profile");
  });
});
