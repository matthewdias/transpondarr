import type { components } from "./api-types";
import { plural } from "./format";

type QueueSearchResult = components["schemas"]["QueueSearchOutputBody"];

// The endpoint queues rather than searches, and under notify-only the run that
// follows rehearses. Both are things a "Searching..." toast would misreport, so
// the wording is derived here and unit-tested rather than written at the call.
export function searchQueuedToast(res: QueueSearchResult) {
  const what =
    res.series_queued < 0
      ? "every series"
      : plural(res.series_queued, "series", "series");
  const caveat =
    res.automation === "notify_only"
      ? " Automation is rehearsing, so nothing will be grabbed."
      : res.automation === "off"
        ? " Automation is off, but this run still searches."
        : "";
  return {
    title: `Search queued for ${what}.${caveat}`,
    description: res.run_triggered
      ? "The sweep is running now and works through its queue a few series per pass."
      : "The next scheduled sweep will pick it up.",
  };
}
