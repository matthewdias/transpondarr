/// <reference types="node" />
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

type RGB = [number, number, number];
type Surface = string | { token: string; alpha: number; over: string };

// Read from disk: vitest blanks CSS imports, `?raw` included.
const css = readFileSync(new URL("../index.css", import.meta.url), "utf8");

function themeTokens(selector: string): Record<string, string> {
  const start = css.indexOf(`${selector} {`);
  const block = css.slice(start, css.indexOf("}", start));
  return Object.fromEntries(
    [...block.matchAll(/--([\w-]+):\s*(#[0-9a-f]{6});/gi)].map((m) => [
      m[1],
      m[2],
    ]),
  );
}

const themes = { light: themeTokens(":root"), dark: themeTokens(".dark") };

function hex(value: string): RGB {
  return [1, 3, 5].map((i) => parseInt(value.slice(i, i + 2), 16)) as RGB;
}

function luminance([r, g, b]: RGB): number {
  const lin = (c: number) => {
    const s = c / 255;
    return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
}

function contrast(a: RGB, b: RGB): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

function resolve(tokens: Record<string, string>, surface: Surface): RGB {
  const color = (name: string) => {
    if (!tokens[name]) throw new Error(`index.css has no --${name}`);
    return hex(tokens[name]);
  };
  if (typeof surface === "string") return color(surface);
  const [fg, bg] = [color(surface.token), color(surface.over)];
  return fg.map(
    (c, i) => c * surface.alpha + bg[i] * (1 - surface.alpha),
  ) as RGB;
}

const label = (s: Surface) =>
  typeof s === "string" ? s : `${s.token}/${s.alpha * 100} over ${s.over}`;

// Tokens that carry small copy, on the surfaces they are laid out over.
const copyTokens = [
  "foreground",
  "muted-foreground",
  "faint",
  "primary",
  "accent-foreground",
  "have",
  "dl",
  "destructive",
];
const plainSurfaces = ["background", "card", "popover", "panel-2"];

// Filled pairs as the components set them: badges, event tones, buttons, the sidebar.
const filledPairs: [string, Surface][] = [
  ["accent-foreground", "accent"],
  ["have", "have-weak"],
  ["dl", "dl-weak"],
  ["destructive", { token: "destructive", alpha: 0.15, over: "card" }],
  ["destructive", { token: "destructive", alpha: 0.05, over: "background" }],
  ["primary-foreground", "primary"],
  ["destructive-foreground", "destructive"],
  ["sidebar-foreground", "sidebar"],
  ["sidebar-accent-foreground", "sidebar-accent"],
];

const pairs: [string, Surface][] = [
  ...copyTokens.flatMap((t) =>
    plainSurfaces.map((s): [string, Surface] => [t, s]),
  ),
  ...filledPairs,
];

// Below WCAG AA today; #163 re-derives these, and each fix must remove its entry.
const knownBelowAA = [
  "light: faint on background (2.77)",
  "light: faint on card (2.95)",
  "light: faint on popover (2.95)",
  "light: faint on panel-2 (2.87)",
  "light: primary on background (3.93)",
  "light: primary on card (4.18)",
  "light: primary on popover (4.18)",
  "light: primary on panel-2 (4.07)",
  "light: have on background (3.10)",
  "light: have on card (3.30)",
  "light: have on popover (3.30)",
  "light: have on panel-2 (3.21)",
  "light: dl on background (3.22)",
  "light: dl on card (3.42)",
  "light: dl on popover (3.42)",
  "light: dl on panel-2 (3.33)",
  "light: have on have-weak (2.86)",
  "light: dl on dl-weak (2.95)",
  "light: destructive on destructive/15 over card (3.82)",
  "light: destructive on destructive/5 over background (4.21)",
  "light: primary-foreground on primary (4.18)",
  "dark: faint on background (4.29)",
  "dark: faint on card (3.96)",
  "dark: faint on popover (3.96)",
  "dark: faint on panel-2 (3.81)",
];

describe("contrast", () => {
  it("matches WCAG reference ratios", () => {
    expect(contrast(hex("#000000"), hex("#ffffff"))).toBeCloseTo(21, 5);
    expect(contrast(hex("#767676"), hex("#ffffff"))).toBeCloseTo(4.54, 2);
    expect(contrast(hex("#777777"), hex("#ffffff"))).toBeCloseTo(4.48, 2);
  });

  it("reads every token in both themes", () => {
    for (const tokens of Object.values(themes)) {
      expect(Object.keys(tokens).length).toBeGreaterThan(30);
    }
  });

  it("holds small copy at AA (4.5:1) in both themes, except the listed debt", () => {
    const below = Object.entries(themes).flatMap(([theme, tokens]) =>
      pairs
        .map(([fg, bg]) => ({
          pair: `${theme}: ${fg} on ${label(bg)}`,
          ratio: contrast(resolve(tokens, fg), resolve(tokens, bg)),
        }))
        .filter(({ ratio }) => ratio < 4.5)
        .map(({ pair, ratio }) => `${pair} (${ratio.toFixed(2)})`),
    );
    expect(below).toEqual(knownBelowAA);
  });
});
