/// <reference types="node" />
import { readdirSync, readFileSync } from "node:fs";
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
  typeof s === "string"
    ? s
    : `${s.token}/${Math.round(s.alpha * 100)} over ${s.over}`;

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
const plainSurfaces = [
  "background",
  "card",
  "popover",
  "panel-2",
  "secondary",
  "sidebar",
];

// Filled pairs as the components set them: badges, event tones, buttons, the sidebar.
const filledPairs: [Surface, Surface][] = [
  ["accent-foreground", "accent"],
  ["have", "have-weak"],
  ["dl", "dl-weak"],
  ["destructive", { token: "destructive", alpha: 0.15, over: "card" }],
  ["destructive", { token: "destructive", alpha: 0.05, over: "background" }],
  ["primary-foreground", "primary"],
  ["destructive-foreground", "destructive"],
  ["sidebar-foreground", "sidebar"],
  // Popover content: the badge explanations and the release score breakdown.
  ["popover-foreground", "popover"],
  ["sidebar-accent-foreground", "sidebar-accent"],
  // The inactive tab trigger and the Wanted item name.
  [{ token: "foreground", alpha: 0.6, over: "muted" }, "muted"],
  [{ token: "foreground", alpha: 0.9, over: "card" }, "card"],
];

// Non-text boundaries at WCAG 1.4.11's 3:1: the focus rings, and the input border,
// whose token is also the unchecked Switch track's fill.
const nonTextPairs: [Surface, Surface][] = [
  "ring",
  "destructive",
  "input",
].flatMap((t) => plainSurfaces.map((s): [Surface, Surface] => [t, s]));

// A focus ring beside a fill of its own colour stays translucent, so it reads as a
// halo apart from that fill; each theme's opacity is measured below.
type Halo = { token: string; alpha: number; fill: number };
const halos: Record<keyof typeof themes, Halo[]> = {
  light: [
    { token: "ring", alpha: 0.75, fill: 1 },
    { token: "destructive", alpha: 0.6, fill: 1 },
  ],
  dark: [
    { token: "ring", alpha: 0.5, fill: 1 },
    { token: "destructive", alpha: 0.85, fill: 0.6 },
  ],
};

const pairs: [Surface, Surface][] = [
  ...copyTokens.flatMap((t) =>
    plainSurfaces.map((s): [Surface, Surface] => [t, s]),
  ),
  ...filledPairs,
];

function below(list: [Surface, Surface][], min: number): string[] {
  return Object.entries(themes).flatMap(([theme, tokens]) =>
    list
      .map(([fg, bg]) => ({
        pair: `${theme}: ${label(fg)} on ${label(bg)}`,
        ratio: contrast(resolve(tokens, fg), resolve(tokens, bg)),
      }))
      .filter(({ ratio }) => ratio < min)
      .map(({ pair, ratio }) => `${pair} (${ratio.toFixed(2)})`),
  );
}

function sources(filter: (f: string) => boolean): string[] {
  const src = new URL("..", import.meta.url);
  return readdirSync(src, { recursive: true, encoding: "utf8" })
    .filter(filter)
    .map((f) => readFileSync(new URL(f, src), "utf8"));
}

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

  it("holds small copy at AA (4.5:1) in both themes", () => {
    expect(below(pairs, 4.5)).toEqual([]);
  });

  it("holds non-text boundaries at 3:1 in both themes", () => {
    expect(below(nonTextPairs, 3)).toEqual([]);
  });

  it("lists every opacity-modified text colour the components use", () => {
    const used = new Set(
      sources((f) => f.endsWith(".tsx") && !f.endsWith(".test.tsx"))
        .flatMap((text) => [...text.matchAll(/\btext-([a-z-]+)\/(\d+)\b/g)])
        .filter((m) => m[1] in themes.light)
        .map((m) => `${m[1]}/${m[2]}`),
    );
    const listed = new Set(
      pairs.flatMap(([fg]) =>
        typeof fg === "string"
          ? []
          : [`${fg.token}/${Math.round(fg.alpha * 100)}`],
      ),
    );
    expect([...used].filter((u) => !listed.has(u))).toEqual([]);
  });

  it("holds focus halos at 3:1 on every surface, apart from their own fill", () => {
    const failing = Object.entries(halos).flatMap(([theme, list]) =>
      list.flatMap(({ token, alpha, fill }) =>
        plainSurfaces.flatMap((over) => {
          const tokens = themes[theme as keyof typeof themes];
          const halo = resolve(tokens, { token, alpha, over });
          const surface = contrast(halo, resolve(tokens, over));
          const apart = contrast(
            halo,
            resolve(tokens, { token, alpha: fill, over }),
          );
          const at = `${theme}: ${token}/${Math.round(alpha * 100)} over ${over}`;
          return [
            ...(surface < 3 ? [`${at} (${surface.toFixed(2)})`] : []),
            // 1.5 is about --border-strong on card: the faintest edge the palette draws.
            ...(apart < 1.5 ? [`${at} vs fill (${apart.toFixed(2)})`] : []),
          ];
        }),
      ),
    );
    expect(failing).toEqual([]);
  });

  it("measures every translucent focus ring colour the components use", () => {
    const listed = new Set(
      Object.entries(halos).flatMap(([theme, list]) =>
        list.map((h) => `${theme}:${h.token}/${Math.round(h.alpha * 100)}`),
      ),
    );
    const unlisted = sources(
      (f) =>
        (f.endsWith(".tsx") && !f.endsWith(".test.tsx")) || f.endsWith(".css"),
    )
      .flatMap((text) => [
        ...text.matchAll(/([\w:[\]=&-]*)(?:ring|outline)-([a-z-]+)\/(\d+)/g),
      ])
      // No page sets aria-invalid yet, so its ring is unreachable; tracked separately.
      .filter((m) => !m[1].includes("aria-invalid:"))
      .map(
        (m) => `${m[1].includes("dark:") ? "dark" : "light"}:${m[2]}/${m[3]}`,
      )
      .filter((ring) => !listed.has(ring));
    expect(unlisted).toEqual([]);
  });

  it("draws no ui focus ring flush at full strength", () => {
    const flush = sources(
      (f) => f.startsWith("components/ui/") && !f.endsWith(".test.tsx"),
    ).flatMap((text) =>
      [
        ...text.matchAll(/focus-visible:ring-(?:ring|destructive)(?![\w/-])/g),
      ].map((m) => m[0]),
    );
    expect(flush).toEqual([]);
  });

  it("fills the unchecked Switch track at full strength", () => {
    expect(
      sources((f) => f.endsWith(".tsx")).flatMap((text) => [
        ...text.matchAll(/data-\[state=unchecked\]:bg-input\/\d+/g),
      ]),
    ).toEqual([]);
  });
});
