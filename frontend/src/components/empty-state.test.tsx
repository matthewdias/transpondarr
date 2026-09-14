import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Tv } from "lucide-react";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/empty-state";

describe("EmptyState", () => {
  it("renders the icon, title, blurb and action it is given", () => {
    const { container } = render(
      <EmptyState
        icon={Tv}
        title="No titles yet"
        as="h2"
        blurb="Add a series or a film."
        action={<Button>Add title</Button>}
      />,
    );
    expect(
      screen.getByRole("heading", { level: 2, name: "No titles yet" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Add a series or a film.")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Add title" }),
    ).toBeInTheDocument();
    expect(container.querySelector("svg")).not.toBeNull();
  });

  it("omits the heading and icon when none are passed", () => {
    const { container } = render(<EmptyState blurb="Nothing downloading." />);
    expect(screen.getByText("Nothing downloading.")).toBeInTheDocument();
    expect(screen.queryByRole("heading")).not.toBeInTheDocument();
    expect(container.querySelector("svg")).toBeNull();
  });

  // Width is the only thing the variant changes, so its class is the contract.
  it("constrains a page-wide empty state and leaves a section's full width", () => {
    const { container, rerender } = render(<EmptyState blurb="x" />);
    const card = () => container.firstElementChild as HTMLElement;
    expect(card()).toHaveClass("mx-auto", "max-w-md");
    expect(screen.queryByRole("heading", { level: 3 })).not.toBeInTheDocument();

    rerender(<EmptyState title="Nothing" blurb="x" width="section" />);
    expect(card()).not.toHaveClass("max-w-md");
    expect(
      screen.getByRole("heading", { level: 3, name: "Nothing" }),
    ).toBeInTheDocument();
  });
});
