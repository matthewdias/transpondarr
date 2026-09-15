import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { ItemStatusBadge, MonitoredBadge } from "@/components/badges";

const sleep = (ms: number) => act(() => new Promise((r) => setTimeout(r, ms)));

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
  it("shows deferred as a distinct batch-downloaded state, not downloading", async () => {
    render(<ItemStatusBadge status="deferred" />);
    expect(screen.queryByText("Downloading")).not.toBeInTheDocument();

    const badge = screen.getByRole("button", { name: "Batch downloaded" });
    expect(badge).not.toHaveAttribute("title");
    await userEvent.click(badge);
    expect(await screen.findByText(/single-episode/)).toBeInTheDocument();
  });

  // A film's deferral is a size tie or an unextracted archive (#210), never a
  // batch, so neither the label nor the advice to grab a single episode applies.
  it("words a deferred film off its item kind rather than off episodes", async () => {
    render(<ItemStatusBadge status="deferred" movie />);

    expect(screen.queryByText("Batch downloaded")).not.toBeInTheDocument();
    const badge = screen.getByRole("button", {
      name: "Downloaded, not imported",
    });
    await userEvent.click(badge);
    const explanation = await screen.findByText(/Activity/);
    expect(explanation).not.toHaveTextContent(/episode/i);
  });

  // Every other status stays byte-identical, which is #210's rule: only the
  // strings shown for a film change.
  it("leaves the other statuses worded as they are for a film", () => {
    render(<ItemStatusBadge status="stuck" movie />);
    expect(screen.getByText("Import blocked")).toBeInTheDocument();

    render(<ItemStatusBadge status="in_library" movie />);
    expect(screen.getByText("In library")).toBeInTheDocument();
  });
});

// Touch and keyboard can't open a title attribute, and no other part of the
// episode row shows the import error.
describe("ItemStatusBadge explanations", () => {
  const error = "import failed: link into the library: permission denied";

  it("opens the import error from the Import blocked badge by click and by keyboard", async () => {
    const user = userEvent.setup();
    render(<ItemStatusBadge status="stuck" error={error} />);
    const badge = screen.getByRole("button", { name: "Import blocked" });
    expect(badge).not.toHaveAttribute("title");

    await user.click(badge);
    expect(await screen.findByText(error)).toBeInTheDocument();
    await user.keyboard("{Escape}");
    await waitFor(() =>
      expect(screen.queryByText(error)).not.toBeInTheDocument(),
    );

    badge.focus();
    await user.keyboard("{Enter}");
    expect(await screen.findByText(error)).toBeInTheDocument();
  });

  it("opens on mouse hover and closes on leave", async () => {
    const user = userEvent.setup();
    render(<ItemStatusBadge status="stuck" error={error} />);
    const badge = screen.getByRole("button", { name: "Import blocked" });

    await user.hover(badge);
    expect(await screen.findByText(error)).toBeInTheDocument();
    await user.unhover(badge);
    await waitFor(() =>
      expect(screen.queryByText(error)).not.toBeInTheDocument(),
    );
  });

  it("keeps a hover-opened explanation open once the badge is clicked", async () => {
    const user = userEvent.setup();
    render(<ItemStatusBadge status="stuck" error={error} />);
    const badge = screen.getByRole("button", { name: "Import blocked" });

    await user.hover(badge);
    await screen.findByText(error);
    await user.click(badge);
    await user.unhover(badge);
    await new Promise((r) => setTimeout(r, 400));
    expect(screen.getByText(error)).toBeInTheDocument();
  });

  // A tap fires pointerover before its click, so opening on it would let the
  // click toggle the explanation straight back closed.
  it("does not open on a touch pointer entering", async () => {
    render(<ItemStatusBadge status="stuck" error={error} />);
    const badge = screen.getByRole("button", { name: "Import blocked" });

    fireEvent.pointerOver(badge, { pointerType: "touch" });
    await new Promise((r) => setTimeout(r, 400));
    expect(screen.queryByText(error)).not.toBeInTheDocument();
  });

  // A toggletip: focus stays on the badge and a live region announces the text,
  // where a dialog took focus and left Tab with nowhere to go (#319 review).
  it("announces the explanation without taking focus from the badge", async () => {
    const user = userEvent.setup();
    render(
      <div>
        <ItemStatusBadge status="stuck" error={error} />
        <button>next</button>
      </div>,
    );
    const badge = screen.getByRole("button", { name: "Import blocked" });
    expect(badge).not.toHaveAttribute("aria-haspopup");

    badge.focus();
    await user.keyboard("{Enter}");
    expect(await screen.findByRole("status")).toHaveTextContent(error);
    expect(badge).toHaveFocus();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

    await user.keyboard("{Escape}");
    await waitFor(() =>
      expect(screen.getByRole("status")).toBeEmptyDOMElement(),
    );

    await user.keyboard("{Enter}");
    await screen.findByText(error);
    await user.tab();
    expect(screen.getByRole("button", { name: "next" })).toHaveFocus();
  });

  it("keeps a click-opened explanation open after the pointer leaves", async () => {
    const user = userEvent.setup();
    render(<ItemStatusBadge status="stuck" error={error} />);
    const badge = screen.getByRole("button", { name: "Import blocked" });

    await user.click(badge);
    await screen.findByText(error);
    await user.unhover(badge);
    await sleep(400);
    expect(screen.getByText(error)).toBeInTheDocument();
  });

  it("stays open while the pointer moves from the badge onto the explanation", async () => {
    const user = userEvent.setup();
    render(<ItemStatusBadge status="stuck" error={error} />);
    const badge = screen.getByRole("button", { name: "Import blocked" });

    await user.hover(badge);
    const explanation = await screen.findByText(error);
    fireEvent.pointerLeave(badge, { pointerType: "mouse" });
    // The pointer crosses the gap between badge and explanation before it enters.
    await sleep(50);
    fireEvent.pointerEnter(explanation, { pointerType: "mouse" });
    await sleep(400);
    expect(screen.getByText(error)).toBeInTheDocument();

    fireEvent.pointerLeave(explanation, { pointerType: "mouse" });
    await waitFor(() =>
      expect(screen.queryByText(error)).not.toBeInTheDocument(),
    );
  });

  // Entering the explanation schedules an open, which Escape must cancel.
  it("does not reopen after Escape while a hover-open is pending", async () => {
    const user = userEvent.setup();
    render(<ItemStatusBadge status="stuck" error={error} />);
    const badge = screen.getByRole("button", { name: "Import blocked" });

    await user.hover(badge);
    const explanation = await screen.findByText(error);
    fireEvent.pointerLeave(badge, { pointerType: "mouse" });
    fireEvent.pointerEnter(explanation, { pointerType: "mouse" });
    await sleep(50);
    await user.keyboard("{Escape}");
    await sleep(500);
    expect(screen.queryByText(error)).not.toBeInTheDocument();
  });

  it("shows one explanation at a time", async () => {
    const user = userEvent.setup();
    render(
      <div>
        <ItemStatusBadge status="stuck" error="first error" />
        <ItemStatusBadge status="deferred" />
      </div>,
    );

    await user.click(screen.getByRole("button", { name: "Import blocked" }));
    await screen.findByText("first error");
    await user.hover(screen.getByRole("button", { name: "Batch downloaded" }));
    expect(await screen.findByText(/single-episode/)).toBeInTheDocument();
    expect(screen.queryByText("first error")).not.toBeInTheDocument();
  });

  // A button inside a link is invalid markup, so the calendar agenda keeps the title attribute.
  it("renders a plain badge with a title when asked to", () => {
    render(<ItemStatusBadge status="stuck" error={error} plain />);
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(screen.getByText("Import blocked")).toHaveAttribute("title", error);
  });

  it("leaves the statuses with nothing to explain non-interactive", () => {
    render(<ItemStatusBadge status="wanted" />);
    render(<ItemStatusBadge status="in_library" />);
    render(<ItemStatusBadge status="downloading" />);
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
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
