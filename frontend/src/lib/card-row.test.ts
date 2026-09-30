/// <reference types="node" />
import { readdirSync, readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { cardRow, cardRowDense } from "@/lib/card-row";

const src = new URL("../", import.meta.url);

// The file that defines a divided row's padding, and so the one place its divider is spelled.
const home = "lib/card-row.ts";

// "file: class" -> why that divider is not a row inside a card.
const allowed: Record<string, string> = {
  "components/ui/table.tsx: [&>tr]:last:border-b-0":
    "upstream shadcn primitive: a table footer's last row",
  "pages/settings/sections/general.tsx: divide-y":
    "a list inset in the section card's padded body, not running to its edges",
  "pages/settings/sections/jobs.tsx: divide-y":
    "a list inset in the section card's padded body, not running to its edges",
};

// A row's own bottom border, a container's divide-y, or ItemGroup's sibling border.
const rowDivider =
  /(?<![\w-])(?:(?:\[[^\]\s"'`]*\]:)?last:border-b-0|divide-y|\[&>\*\+\*\]:border-t)(?![\w-])/g;

function sources(): string[] {
  return readdirSync(src, { recursive: true, encoding: "utf8" }).filter(
    (f) => /\.tsx?$/.test(f) && !/\.test\.tsx?$/.test(f) && f !== home,
  );
}

function rowDividers(): { site: string; key: string }[] {
  return sources().flatMap((file) =>
    readFileSync(new URL(file, src), "utf8")
      .split("\n")
      .flatMap((line, i) =>
        [...line.matchAll(rowDivider)].map((m) => ({
          site: `${file}:${i + 1} ${m[0]}`,
          key: `${file}: ${m[0]}`,
        })),
      ),
  );
}

describe("card rows", () => {
  it("spells the row divider only in the shared row classes", () => {
    const literal = rowDividers()
      .filter(({ key }) => !(key in allowed))
      .map(({ site }) => site);
    expect(literal).toEqual([]);
  });

  it("keeps the shared row classes on the agreed padding", () => {
    const padding = (classes: string) =>
      classes.split(" ").filter((c) => /^p[xy]?-/.test(c));
    expect(padding(cardRow)).toEqual(["px-4", "py-3"]);
    expect(padding(cardRowDense)).toEqual(["px-4", "py-2"]);
  });

  it("allows only dividers still in use", () => {
    const used = new Set(rowDividers().map(({ key }) => key));
    expect(Object.keys(allowed).filter((key) => !used.has(key))).toEqual([]);
  });
});
