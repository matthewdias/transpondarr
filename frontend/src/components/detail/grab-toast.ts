import type { GrabResult } from "@/lib/api";

export function grabToast(res: GrabResult) {
  return res.ineligible_reason
    ? {
        level: "warning" as const,
        title: "Grabbed despite the profile",
        description: `${res.release} · ${res.ineligible_reason}`,
      }
    : {
        level: "success" as const,
        title: "Grab sent to download client",
        description: `${res.release} · ${res.outcome}`,
      };
}
