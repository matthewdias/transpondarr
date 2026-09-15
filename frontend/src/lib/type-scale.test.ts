/// <reference types="node" />
import { readdirSync, readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const src = new URL("../", import.meta.url);

// "file: class" -> why that size stays off the scale.
const allowed: Record<string, string> = {};

const arbitrarySize = /\btext-\[(?:length:)?\d*\.?\d+(?:px|rem|em)\]/g;

function sources(): string[] {
  return readdirSync(src, { recursive: true, encoding: "utf8" }).filter(
    (f) => /\.tsx?$/.test(f) && !/\.test\.tsx?$/.test(f),
  );
}

function arbitrarySizes(): { site: string; key: string }[] {
  return sources().flatMap((file) =>
    readFileSync(new URL(file, src), "utf8")
      .split("\n")
      .flatMap((line, i) =>
        [...line.matchAll(arbitrarySize)].map((m) => ({
          site: `${file}:${i + 1} ${m[0]}`,
          key: `${file}: ${m[0]}`,
        })),
      ),
  );
}

describe("type scale", () => {
  it("sets no font size between the theme's steps", () => {
    const offScale = arbitrarySizes()
      .filter(({ key }) => !(key in allowed))
      .map(({ site }) => site);
    expect(offScale).toEqual([]);
  });

  it("allows only sizes still in use", () => {
    const used = new Set(arbitrarySizes().map(({ key }) => key));
    expect(Object.keys(allowed).filter((key) => !used.has(key))).toEqual([]);
  });

  it("defines the 2xs step the annotations use", () => {
    const css = readFileSync(new URL("index.css", src), "utf8");
    expect(css).toMatch(/--text-2xs:\s*0\.6875rem;/);
    expect(css).toMatch(/--text-2xs--line-height:/);
  });
});
