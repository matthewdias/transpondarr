import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ItemStatusBadge, MonitoredBadge } from "@/components/badges";

describe("ItemStatusBadge", () => {
  it("labels each status", () => {
    render(<ItemStatusBadge status="in_library" />);
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

  // A film's deferral is a size tie or an unextracted archive (#210), never a
  // batch, so neither the label nor the advice to grab a single episode holds.
  it("words a deferred film off its kind rather than off episodes", () => {
    render(<ItemStatusBadge status="deferred" movie />);

    expect(screen.queryByText("Batch downloaded")).not.toBeInTheDocument();
    const badge = screen.getByText("Downloaded, not imported");
    expect(badge.getAttribute("title")).not.toMatch(/episode/i);
    expect(badge).toHaveAttribute("title", expect.stringContaining("Activity"));
  });

  // Every other status stays byte-identical, which is #210's rule: only the
  // strings a film can reach change.
  it("leaves the other statuses worded as they are for a film", () => {
    render(<ItemStatusBadge status="stuck" movie />);
    expect(screen.getByText("Import blocked")).toBeInTheDocument();

    render(<ItemStatusBadge status="in_library" movie />);
    expect(screen.getByText("In library")).toBeInTheDocument();
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
