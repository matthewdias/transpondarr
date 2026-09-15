/// <reference types="node" />
import { readdirSync, readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const src = new URL("../", import.meta.url);

// "file: class" -> why that radius stays off the theme's tokens.
const allowed: Record<string, string> = {
  "components/ui/checkbox.tsx: rounded-[4px]":
    "upstream shadcn primitive, kept in step with the registry",
  "components/ui/tooltip.tsx: rounded-[2px]":
    "upstream shadcn primitive: the arrow's corner, not a surface",
};

// Any arbitrary radius utility, any side or corner, bracket or parenthesis form.
const arbitraryRadius =
  /(?<![\w-])rounded(?:-(?:t|r|b|l|s|e|tl|tr|br|bl|ss|se|es|ee))?-(\[[^\]\s"'`]*\]|\([^)\s"'`]*\))/g;

function sources(): string[] {
  return readdirSync(src, { recursive: true, encoding: "utf8" }).filter(
    (f) => /\.tsx?$/.test(f) && !/\.test\.tsx?$/.test(f),
  );
}

function arbitraryRadii(): { site: string; key: string }[] {
  return sources().flatMap((file) =>
    readFileSync(new URL(file, src), "utf8")
      .split("\n")
      .flatMap((line, i) =>
        [...line.matchAll(arbitraryRadius)].map((m) => ({
          site: `${file}:${i + 1} ${m[0]}`,
          key: `${file}: ${m[0]}`,
        })),
      ),
  );
}

describe("radius scale", () => {
  it("sets no radius outside the theme's tokens", () => {
    const offScale = arbitraryRadii()
      .filter(({ key }) => !(key in allowed))
      .map(({ site }) => site);
    expect(offScale).toEqual([]);
  });

  it("allows only radii still in use", () => {
    const used = new Set(arbitraryRadii().map(({ key }) => key));
    expect(Object.keys(allowed).filter((key) => !used.has(key))).toEqual([]);
  });
});
