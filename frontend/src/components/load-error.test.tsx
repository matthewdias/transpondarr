import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api";
import { LoadError } from "@/components/load-error";

describe("LoadError", () => {
  it("names what failed to load, or says what failed in its own words", () => {
    const error = new ApiError(502, "indexer timed out");
    const { rerender } = render(
      <LoadError what="the calendar" error={error} onRetry={vi.fn()} />,
    );
    expect(
      screen.getByText("Couldn’t load the calendar. indexer timed out"),
    ).toBeInTheDocument();

    rerender(
      <LoadError
        message="Couldn’t search for releases."
        error={error}
        onRetry={vi.fn()}
      />,
    );
    expect(
      screen.getByText("Couldn’t search for releases. indexer timed out"),
    ).toBeInTheDocument();
  });
});
