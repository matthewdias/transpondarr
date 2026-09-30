import type { QueryClient } from "@tanstack/react-query";
import { act } from "@testing-library/react";
import { onTestFinished, vi } from "vitest";

// setTimeout stays real for MSW. The useNow tick and React Query's polls run on
// setInterval, and so does findBy's polling, so under this it waits on DOM changes.
export function fakeClock(at: number | Date = Date.now()) {
  vi.useFakeTimers({ toFake: ["setInterval", "clearInterval", "Date"] });
  vi.setSystemTime(at);
  onTestFinished(() => {
    vi.useRealTimers();
  });
}

export function advanceClock(ms: number) {
  act(() => {
    vi.advanceTimersByTime(ms);
  });
}

// A poll the clock fired can outlive the test, and unmounting aborts it with a
// socket hang up. vi.waitFor, since Testing Library's waitFor polls on setInterval.
export async function settle(client: QueryClient) {
  await vi.waitFor(() => {
    if (client.isFetching() > 0) throw new Error("a query is still fetching");
  });
}
