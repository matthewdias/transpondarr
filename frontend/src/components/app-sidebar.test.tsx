import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { beforeEach, expect, it } from "vitest";
import { AppSidebar } from "@/components/app-sidebar";
import { ThemeProvider } from "@/components/theme-provider";
import { SidebarProvider } from "@/components/ui/sidebar";

beforeEach(() => {
  localStorage.clear();
  document.documentElement.classList.remove("dark");
});

// Seeded rather than served: the two queries only supply a badge count and the
// sign-out row, and a never-stale cache keeps the mount off the network.
function renderSidebar() {
  const client = new QueryClient({
    defaultOptions: { queries: { staleTime: Infinity, retry: false } },
  });
  client.setQueryData(["titles"], []);
  client.setQueryData(["auth-status"], {
    configured: true,
    required: "enabled",
    authenticated: true,
    session: true,
    username: "dev",
    local: false,
  });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <ThemeProvider>
          <SidebarProvider>
            <AppSidebar />
          </SidebarProvider>
        </ThemeProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

it("offers the three theme states as a labelled radio group", async () => {
  renderSidebar();
  const user = userEvent.setup();
  const group = screen.getByRole("radiogroup", { name: "Theme" });
  const option = (name: string) => within(group).getByRole("radio", { name });

  // No visible text, so WCAG 2.5.3 does not apply: what must hold instead is that
  // the accessible name matches the label a pointer reveals.
  for (const label of ["Light", "Dark", "System"])
    expect(option(label)).toHaveAttribute("title", label);

  expect(option("System")).toBeChecked();
  expect(document.documentElement).not.toHaveClass("dark");

  await user.click(option("Dark"));
  expect(option("Dark")).toBeChecked();
  expect(option("System")).not.toBeChecked();
  expect(document.documentElement).toHaveClass("dark");

  // A radio never clears itself, where a toggle would.
  await user.click(option("Dark"));
  expect(option("Dark")).toBeChecked();

  // An arrow moves the focus and leaves the theme alone, so passing over Light
  // on the way to System doesn't repaint the app twice. Space is what picks.
  await user.keyboard("{ArrowLeft}");
  expect(option("Light")).toHaveFocus();
  expect(option("Dark")).toBeChecked();
  await user.keyboard(" ");
  expect(option("Light")).toBeChecked();
  expect(document.documentElement).not.toHaveClass("dark");

  await user.click(option("System"));
  expect(option("System")).toBeChecked();
  expect(option("Light")).not.toBeChecked();
});
