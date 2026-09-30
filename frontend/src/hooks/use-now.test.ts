import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { NOW_TICK_MS, useNow } from "@/hooks/use-now";

const T0 = Date.parse("2026-07-23T12:00:00Z");

describe("useNow", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(T0);
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("returns the mount time and advances only on the shared tick", () => {
    const { result } = renderHook(() => useNow());
    expect(result.current).toBe(T0);

    act(() => vi.advanceTimersByTime(NOW_TICK_MS - 1));
    expect(result.current).toBe(T0);

    act(() => vi.advanceTimersByTime(1));
    expect(result.current).toBe(T0 + NOW_TICK_MS);
  });

  // A component mounted between ticks starts from its own mount time, so it
  // never renders a time from before it mounted.
  it("starts a late subscriber at its own mount time", () => {
    renderHook(() => useNow());
    act(() => vi.advanceTimersByTime(NOW_TICK_MS / 2));
    const { result } = renderHook(() => useNow());
    expect(result.current).toBe(T0 + NOW_TICK_MS / 2);
  });

  // An aired episode's date never changes again, so a long episode table
  // keeps ticking only the rows still counting down.
  it("stops ticking once until has passed, and never starts past it", () => {
    const { result } = renderHook(() => useNow(T0 + NOW_TICK_MS * 1.5));
    expect(vi.getTimerCount()).toBe(1);

    act(() => vi.advanceTimersByTime(NOW_TICK_MS * 2));
    expect(result.current).toBe(T0 + NOW_TICK_MS * 2);
    expect(vi.getTimerCount()).toBe(0);

    renderHook(() => useNow(T0));
    expect(vi.getTimerCount()).toBe(0);
  });

  it("runs one timer however many components read it, and none after they unmount", () => {
    const a = renderHook(() => useNow());
    const b = renderHook(() => useNow());
    expect(vi.getTimerCount()).toBe(1);

    act(() => vi.advanceTimersByTime(NOW_TICK_MS));
    expect(a.result.current).toBe(b.result.current);

    a.unmount();
    expect(vi.getTimerCount()).toBe(1);
    b.unmount();
    expect(vi.getTimerCount()).toBe(0);
  });
});
