import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useDebounce } from "@/hooks/use-debounce";

describe("useDebounce", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("returns the initial value immediately", () => {
    const { result } = renderHook(() => useDebounce("a"));
    expect(result.current).toBe("a");
  });

  it("holds the previous value until the delay elapses", () => {
    const { result, rerender } = renderHook(
      ({ value }) => useDebounce(value, 300),
      {
        initialProps: { value: "a" },
      },
    );
    rerender({ value: "b" });
    act(() => vi.advanceTimersByTime(299));
    expect(result.current).toBe("a");
    act(() => vi.advanceTimersByTime(1));
    expect(result.current).toBe("b");
  });

  it("collapses rapid changes into the last value only", () => {
    const { result, rerender } = renderHook(
      ({ value }) => useDebounce(value, 300),
      {
        initialProps: { value: "e" },
      },
    );
    rerender({ value: "ex" });
    act(() => vi.advanceTimersByTime(200));
    rerender({ value: "exa" });
    act(() => vi.advanceTimersByTime(200));
    rerender({ value: "example" });
    expect(result.current).toBe("e");
    act(() => vi.advanceTimersByTime(300));
    expect(result.current).toBe("example");
  });

  it("respects a custom delay", () => {
    const { result, rerender } = renderHook(
      ({ value }) => useDebounce(value, 50),
      {
        initialProps: { value: 1 },
      },
    );
    rerender({ value: 2 });
    act(() => vi.advanceTimersByTime(50));
    expect(result.current).toBe(2);
  });
});
