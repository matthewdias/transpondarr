import { airDate, parseTimestamp } from "@/lib/format";
import { useNow } from "@/hooks/use-now";

/** A broadcast time that counts down on the shared tick, and stops ticking once aired. */
export function AirDate({ at }: { at: string }) {
  const now = useNow(parseTimestamp(at));
  return airDate(at, now);
}
