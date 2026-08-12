import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import {
  afterAll,
  afterEach,
  beforeAll,
  describe,
  expect,
  it,
  vi,
} from "vitest";
import {
  GroupCell,
  ReleasesTab,
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

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const focusResults = [
  release({
    title: "[GroupA] Example Show - 03 (1080p)",
    download_url: "magnet:?xt=urn:btih:0003",
    items: [3],
  }),
  release({
    title: "[GroupB] Example Show 01-12 (1080p)",
    download_url: "magnet:?xt=urn:btih:0112",
    items: [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12],
  }),
  release({
    title: "[GroupA] Example Show - 05 (1080p)",
    download_url: "magnet:?xt=urn:btih:0005",
    items: [5],
  }),
  release({
    title: "[GroupC] Other Show - 03 (1080p)",
    download_url: "magnet:?xt=urn:btih:9999",
    matched: false,
    reason: "release is for a different series",
    items: undefined,
  }),
];

// A gated handler holds the search in flight so the loading header can be
// asserted, then lets it land in the same test rather than leaving a dangling
// request for teardown to reset.
function renderReleases(focusItem: number | null, gated = false) {
  let land = () => {};
  const inFlight = new Promise<void>((resolve) => {
    land = resolve;
  });
  server.use(
    http.get("/api/v1/titles/7/search", async () => {
      if (gated) await inFlight;
      return HttpResponse.json({
        series: "Example Show",
        results: focusResults,
      });
    }),
  );
  const onClearFocus = vi.fn();
  const user = userEvent.setup();
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <ReleasesTab
        seriesId={7}
        active
        focusItem={focusItem}
        onClearFocus={onClearFocus}
      />
    </QueryClientProvider>,
  );
  return { onClearFocus, user, land };
}

describe("ReleasesTab episode focus", () => {
  it("shows only the releases covering the focused episode", async () => {
    const { onClearFocus, user } = renderReleases(3);

    expect(
      await screen.findByText("[GroupA] Example Show - 03 (1080p)"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("[GroupB] Example Show 01-12 (1080p)"),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("[GroupA] Example Show - 05 (1080p)"),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByText("[GroupC] Other Show - 03 (1080p)"),
    ).not.toBeInTheDocument();
    expect(screen.getByText("2 of 4 results")).toBeInTheDocument();

    // The chip is the way out of the filter, so it has to name the action and
    // not just the state it reads as on screen.
    await user.click(
      screen.getByRole("button", { name: /covering e3.*clear filter/i }),
    );
    expect(onClearFocus).toHaveBeenCalledTimes(1);
  });

  // "0 of 0" during the initial search reads as a finished search that found
  // nothing, which is the opposite of what is happening.
  it("withholds the count until the search has landed", async () => {
    const { land } = renderReleases(3, true);

    expect(await screen.findByText(/searching indexers/i)).toBeInTheDocument();
    expect(screen.queryByText(/0 of 0 results/)).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /covering e3.*clear filter/i }),
    ).toBeInTheDocument();

    land();
    expect(await screen.findByText("2 of 4 results")).toBeInTheDocument();
  });

  // Nothing covering the episode is not "nothing found for this series" — the
  // way out is the filter, so the empty state has to offer it.
  it("offers the full list when nothing covers the focused episode", async () => {
    const { onClearFocus, user } = renderReleases(20);

    expect(
      await screen.findByText(/no releases cover e20/i),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(/no releases found for this series/i),
    ).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /show all/i }));
    expect(onClearFocus).toHaveBeenCalledTimes(1);
  });

  it("leaves the series-wide view untouched with no focus", async () => {
    renderReleases(null);

    expect(
      await screen.findByText("[GroupA] Example Show - 05 (1080p)"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("[GroupC] Other Show - 03 (1080p)"),
    ).toBeInTheDocument();
    expect(screen.queryByText(/covering e/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/of 4 results/)).not.toBeInTheDocument();
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
