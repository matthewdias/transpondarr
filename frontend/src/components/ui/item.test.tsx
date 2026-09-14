import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Item, ItemGroup } from "@/components/ui/item";

describe("Item", () => {
  it("is a list item inside a group", () => {
    render(
      <ItemGroup>
        <Item>one</Item>
        <Item>two</Item>
      </ItemGroup>,
    );
    expect(screen.getByRole("list")).toBeInTheDocument();
    expect(screen.getAllByRole("listitem")).toHaveLength(2);
  });

  // A listitem outside a list is its own ARIA violation.
  it("has no list role on its own", () => {
    render(<Item>alone</Item>);
    expect(screen.queryByRole("listitem")).not.toBeInTheDocument();
  });
});
