import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { Poster } from "@/components/poster";

// Cover art is decorative (alt=""), so it has no accessible name to query by.
const cover = (container: HTMLElement) =>
  container.querySelector("img") as HTMLImageElement | null;

it("falls back to the initial letter when the cover fails to load", () => {
  const { container } = render(
    <Poster title="Placeholder Saga" coverUrl="https://cdn.example/gone.jpg" />,
  );

  fireEvent.error(cover(container)!);

  expect(cover(container)).toBeNull();
  expect(screen.getByText("P")).toBeInTheDocument();
});

it("tries the new cover after the previous one failed", () => {
  const { container, rerender } = render(
    <Poster title="Placeholder Saga" coverUrl="https://cdn.example/gone.jpg" />,
  );
  fireEvent.error(cover(container)!);

  rerender(
    <Poster title="Placeholder Saga" coverUrl="https://cdn.example/new.jpg" />,
  );

  expect(cover(container)).toHaveAttribute(
    "src",
    "https://cdn.example/new.jpg",
  );
});
