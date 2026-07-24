import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { HistoryRow } from "@/components/detail/history-tab";
import type { GrabEvent } from "@/lib/api";

function event(overrides: Partial<GrabEvent>): GrabEvent {
  return {
    id: 1,
    item_number: 3,
    infohash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    release_title: "[FakeGroup] Example Show - 03 (1080p) [ABCD1234].mkv",
    status: "grabbed",
    created_at: new Date().toISOString(),
    ...overrides,
  };
}

describe("HistoryRow", () => {
  it("shows an in-progress grab as Downloading", () => {
    render(<HistoryRow event={event({ status: "grabbed" })} />);
    expect(screen.getByText(/Downloading/)).toBeInTheDocument();
  });

  // Regression pin from issue #47: a terminal failure must not read as in-progress.
  it("shows a failed grab as Failed, not Downloading", () => {
    render(<HistoryRow event={event({ status: "failed" })} />);
    expect(screen.queryByText(/Downloading/)).not.toBeInTheDocument();
    expect(screen.getByText(/Failed/)).toBeInTheDocument();
  });

  it("shows a grab with a pending import error as Import blocked", () => {
    render(
      <HistoryRow
        event={event({
          status: "grabbed",
          last_error: "import failed: disk full",
        })}
      />,
    );
    expect(screen.getByText(/Import blocked/)).toBeInTheDocument();
    expect(screen.getByText("import failed: disk full")).toBeInTheDocument();
  });
});
