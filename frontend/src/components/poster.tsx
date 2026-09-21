import { useCoverFallback } from "@/hooks/use-cover-fallback";
import { cn } from "@/lib/utils";

/** First-letter placeholder, for AniList cover art absent or failing to load. */
function initial(title: string): string {
  return title.trim().charAt(0).toUpperCase() || "?";
}

export function Poster({
  title,
  coverUrl,
  size = "sm",
  className,
}: {
  title: string;
  coverUrl?: string;
  size?: "sm" | "lg";
  className?: string;
}) {
  const { showCover, onCoverError } = useCoverFallback(coverUrl);
  const dims =
    size === "lg"
      ? "w-20 h-28 text-3xl rounded-lg"
      : "w-[34px] h-12 text-base rounded-sm";
  if (showCover) {
    return (
      <img
        src={coverUrl}
        alt=""
        onError={onCoverError}
        className={cn("flex-none border object-cover", dims, className)}
      />
    );
  }
  return (
    <div
      className={cn(
        "flex-none grid place-items-center border border-border bg-gradient-to-br from-accent to-panel-2 font-bold text-accent-foreground",
        dims,
        className,
      )}
    >
      {initial(title)}
    </div>
  );
}
