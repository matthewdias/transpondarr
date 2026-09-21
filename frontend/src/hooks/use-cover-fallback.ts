import { useState } from "react";

/** The failed URL, not a flag, so a title whose cover art changes is tried again. */
export function useCoverFallback(coverUrl?: string) {
  const [failed, setFailed] = useState<string>();
  return {
    showCover: Boolean(coverUrl) && coverUrl !== failed,
    onCoverError: () => setFailed(coverUrl),
  };
}
