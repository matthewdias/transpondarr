import type { ComponentProps } from "react";

/** Canonical AniList outlink: one href/rel/label rule for every surface. */
export function AniListLink({
  id,
  ...props
}: { id: number } & Omit<ComponentProps<"a">, "id">) {
  return (
    <a
      {...props}
      href={`https://anilist.co/anime/${id}`}
      target="_blank"
      rel="noopener noreferrer"
      aria-label="Open on AniList"
      title="Open on AniList"
    />
  );
}
