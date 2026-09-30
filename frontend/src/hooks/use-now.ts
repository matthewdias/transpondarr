import { useEffect, useState } from "react";

// Labels are minute-granular, so a 30s tick bounds their lag without wasted renders.
export const NOW_TICK_MS = 30_000;

const listeners = new Set<(now: number) => void>();
let timer: ReturnType<typeof setInterval> | undefined;

function subscribe(listener: (now: number) => void): () => void {
  listeners.add(listener);
  if (listeners.size === 1) {
    timer = setInterval(() => {
      const now = Date.now();
      for (const l of listeners) l(now);
    }, NOW_TICK_MS);
  }
  return () => {
    listeners.delete(listener);
    if (listeners.size === 0) {
      clearInterval(timer);
      timer = undefined;
    }
  };
}

/**
 * The current time in epoch ms, re-read for every caller on one shared tick.
 * A caller whose label stops changing at a known instant passes it as `until`.
 */
export function useNow(until = Infinity): number {
  const [now, setNow] = useState(Date.now);
  const live = now < until;
  useEffect(() => (live ? subscribe(setNow) : undefined), [live]);
  return now;
}
