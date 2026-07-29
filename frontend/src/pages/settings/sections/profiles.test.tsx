import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ExcludePicker } from "@/pages/settings/sections/profiles";

describe("ExcludePicker", () => {
  it("offers only axis values the parser can emit", () => {
    render(<ExcludePicker excludes={[]} stale={[]} onChange={() => {}} />);
    for (const label of [
      "Hardsub",
      "Softsub",
      "H.265 / HEVC",
      "WEB",
      "2160p",
      "576p",
      "360p",
    ]) {
      expect(screen.getByRole("button", { name: label })).toBeInTheDocument();
    }
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();

    // Same mount: the copy is part of what the picker presents, and a second
    // render of it costs more than the assertions do.
    expect(screen.getByText(/read from the release name/i)).toBeInTheDocument();
    expect(
      screen.getByText(/does not label|doesn’t label/i),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/trusted groups|minimum score/i),
    ).toBeInTheDocument();
  });

  it("shows a matchable resolution it does not offer, so nothing is invisible", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <ExcludePicker excludes={["544p"]} stale={[]} onChange={onChange} />,
    );
    const chip = screen.getByRole("button", { name: "544p" });
    expect(chip).toHaveAttribute("aria-pressed", "true");
    await user.click(chip);
    expect(onChange).toHaveBeenLastCalledWith([]);
  });

  it("toggles a value on and off through onChange", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const { rerender } = render(
      <ExcludePicker excludes={[]} stale={[]} onChange={onChange} />,
    );
    await user.click(screen.getByRole("button", { name: "Hardsub" }));
    expect(onChange).toHaveBeenLastCalledWith(["hardsub"]);

    rerender(
      <ExcludePicker excludes={["hardsub"]} stale={[]} onChange={onChange} />,
    );
    const chip = screen.getByRole("button", { name: "Hardsub" });
    expect(chip).toHaveAttribute("aria-pressed", "true");
    await user.click(chip);
    expect(onChange).toHaveBeenLastCalledWith([]);
  });

  it("flags a stored token that can never match and lets it be removed", async () => {
    const user = userEvent.setup();
    const onStaleChange = vi.fn();
    render(
      <ExcludePicker
        excludes={[]}
        stale={["dub"]}
        onChange={() => {}}
        onStaleChange={onStaleChange}
      />,
    );
    expect(screen.getByText("dub")).toBeInTheDocument();
    expect(screen.getByText(/never match/i)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /remove dub/i }));
    expect(onStaleChange).toHaveBeenCalledWith([]);
  });
});
