import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ItemStatusBadge, MonitoredBadge } from "@/components/badges";

describe("ItemStatusBadge", () => {
  it("labels each status", () => {
    render(<ItemStatusBadge status="have" />);
    expect(screen.getByText("In library")).toBeInTheDocument();

    render(<ItemStatusBadge status="downloading" />);
    expect(screen.getByText("Downloading")).toBeInTheDocument();

    render(<ItemStatusBadge status="wanted" />);
    expect(screen.getByText("Wanted")).toBeInTheDocument();
  });

  // Regression pin from PR #34: a deferred batch must not read as "Downloading".
  it("shows deferred as a distinct batch-downloaded state, not downloading", () => {
    render(<ItemStatusBadge status="deferred" />);
    expect(screen.queryByText("Downloading")).not.toBeInTheDocument();
    const badge = screen.getByText("Batch downloaded");
    expect(badge).toHaveAttribute(
      "title",
      expect.stringContaining("single-episode"),
    );
  });
});

describe("MonitoredBadge", () => {
  it("labels both monitored states", () => {
    render(<MonitoredBadge monitored={true} />);
    expect(screen.getByText("Monitored")).toBeInTheDocument();

    render(<MonitoredBadge monitored={false} />);
    expect(screen.getByText("Unmonitored")).toBeInTheDocument();
  });
});
