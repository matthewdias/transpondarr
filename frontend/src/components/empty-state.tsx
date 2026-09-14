import type { ReactNode } from "react";
import type { LucideIcon } from "lucide-react";
import { cn } from "@/lib/utils";

/** A view with nothing to list, in the dashed card `LoadError` also uses. */
export function EmptyState({
  icon: Icon,
  title,
  blurb,
  action,
  width = "page",
  as: Heading = "h3",
}: {
  icon?: LucideIcon;
  title?: string;
  blurb: ReactNode;
  action?: ReactNode;
  // "section" fills the width of the list it replaces under a section heading.
  width?: "page" | "section";
  as?: "h2" | "h3";
}) {
  return (
    <div
      className={cn(
        "flex flex-col items-center rounded-lg border border-dashed bg-card px-6 py-12 text-center",
        width === "page" && "mx-auto max-w-md",
      )}
    >
      {Icon && <Icon className="mb-3 size-6 text-faint" />}
      {title && <Heading className="text-sm font-semibold">{title}</Heading>}
      <p
        className={cn(
          "max-w-md text-sm text-muted-foreground",
          title && "mt-1.5",
        )}
      >
        {blurb}
      </p>
      {action && <div className="mt-4">{action}</div>}
    </div>
  );
}
