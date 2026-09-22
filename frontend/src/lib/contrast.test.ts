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
type Theme = keyof typeof themes;

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

// `text-white` is a Tailwind literal rather than a token, so `themeTokens` never reads it.
const literalColors: Record<string, string> = { white: "#ffffff" };

function resolve(tokens: Record<string, string>, surface: Surface): RGB {
  const color = (name: string) => {
    const value = literalColors[name] ?? tokens[name];
    if (!value) throw new Error(`index.css has no --${name}`);
    return hex(value);
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
// halo apart from that fill. `fills` lists every opacity that fill takes while focused.
type Halo = { token: string; alpha: number; fills: number[] };
const halos: Record<Theme, Halo[]> = {
  light: [
    { token: "ring", alpha: 0.75, fills: [1] },
    { token: "destructive", alpha: 0.6, fills: [1, 0.9] },
  ],
  dark: [
    { token: "ring", alpha: 0.5, fills: [1] },
    { token: "destructive", alpha: 0.85, fills: [0.6] },
  ],
};

const sharedPairs: [Surface, Surface][] = [
  ...copyTokens.flatMap((t) =>
    plainSurfaces.map((s): [Surface, Surface] => [t, s]),
  ),
  ...filledPairs,
];

// The destructive button and badge draw `text-white` on a fill that `dark:bg-destructive/60`
// changes per theme, so no single pair measures what both themes draw.
const pairs: Record<Theme, [Surface, Surface][]> = {
  light: [...sharedPairs, ["white", "destructive"]],
  dark: [
    ...sharedPairs,
    ["white", { token: "destructive", alpha: 0.6, over: "card" }],
  ],
};

type PairList = [Surface, Surface][] | Record<Theme, [Surface, Surface][]>;

function below(list: PairList, min: number): string[] {
  return Object.entries(themes).flatMap(([theme, tokens]) =>
    (Array.isArray(list) ? list : list[theme as Theme])
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

const pct = (alpha: number) => Math.round(alpha * 100);

// One class list per string literal, or per @apply in CSS: the unit a component styles.
function classSources(): { file: string; lists: string[][] }[] {
  const src = new URL("..", import.meta.url);
  return readdirSync(src, { recursive: true, encoding: "utf8" })
    .filter((f) => /\.(tsx?|css)$/.test(f) && !/\.test\.tsx?$/.test(f))
    .map((file) => {
      const text = readFileSync(new URL(file, src), "utf8");
      const literals = file.endsWith(".css")
        ? [...text.matchAll(/@apply\s+([^;]+);/g)].map((m) => m[1])
        : [...text.matchAll(/"([^"\n]*)"|'([^'\n]*)'|`([^`]*)`/g)].map(
            (m) => m[1] ?? m[2] ?? m[3],
          );
      return {
        file,
        lists: literals.map((l) => l.split(/\s+/).filter(Boolean)),
      };
    });
}

type Parsed = {
  variants: string[];
  kind: "ring" | "outline" | "bg";
  value: string;
  alpha: number | null;
};

// Splits Tailwind variants on top-level colons, so `/`, `*` and brackets inside a variant survive.
function parseClass(cls: string): Parsed | null {
  const parts: string[] = [];
  let depth = 0;
  let start = 0;
  for (let i = 0; i < cls.length; i++) {
    if ("[(".includes(cls[i])) depth++;
    else if ("])".includes(cls[i])) depth--;
    else if (cls[i] === ":" && depth === 0) {
      parts.push(cls.slice(start, i));
      start = i + 1;
    }
  }
  const utility = cls.slice(start).replace(/^!|!$/g, "");
  const m = /^(ring|outline|bg)-(.+?)(?:\/(\d+|\[[^\]]+\]|\([^)]+\)))?$/.exec(
    utility,
  );
  if (!m) return null;
  const [, kind, value, opacity] = m;
  if (
    /^(\d|offset|inset|none|hidden|solid|dashed|dotted|double|\[(\d|length:|calc)|\((length|number):)/.test(
      value,
    )
  ) {
    return null;
  }
  let alpha: number | null = 1;
  if (opacity !== undefined) {
    const n = /^\[?(\d*\.?\d+)(%?)\]?$/.exec(opacity);
    alpha = !n
      ? null
      : n[2] || !opacity.startsWith("[")
        ? Number(n[1]) / 100
        : Number(n[1]);
  }
  return { variants: parts, kind: kind as Parsed["kind"], value, alpha };
}

// Which class wins in a theme: more variants is more specific, and `dark:` is emitted after
// the built-in variants, so it wins a tie.
function rank(c: Parsed): [number, number] {
  return [c.variants.length, c.variants.includes("dark") ? 1 : 0];
}
const outranks = (a: Parsed, b: Parsed) => {
  const [ra, rb] = [rank(a), rank(b)];
  return ra[0] > rb[0] || (ra[0] === rb[0] && ra[1] > rb[1]);
};
const inTheme = (c: Parsed, theme: Theme) =>
  theme === "dark" || !c.variants.includes("dark");
const states = (c: Parsed) => c.variants.filter((v) => v !== "dark");

// What a class list draws while focused in the state `c` targets, among classes of `kind`.
function winner(
  list: Parsed[],
  c: Parsed,
  theme: Theme,
  kind: Parsed["kind"],
): Parsed {
  const reach = new Set([...states(c), "focus-visible"]);
  return list
    .filter(
      (d) =>
        d.kind === kind &&
        inTheme(d, theme) &&
        states(d).every((v) => reach.has(v)),
    )
    .reduce((best, d) => (outranks(d, best) ? d : best), c);
}

function focusRingProblems(
  files: { file: string; lists: string[][] }[],
): string[] {
  const problems: string[] = [];
  const seen = new Map<Halo, Set<number>>();
  for (const { file, lists } of files) {
    const parsedLists = lists.map((l) =>
      l
        // No page sets aria-invalid yet, so its ring is unreachable; tracked separately.
        .filter((cls) => !cls.includes("aria-invalid"))
        .map((cls) => ({ cls, parsed: parseClass(cls) }))
        .filter((c): c is { cls: string; parsed: Parsed } => !!c.parsed),
    );
    for (const [i, list] of parsedLists.entries()) {
      // A ring whose width is set outside focus, like a progress bar's ring-1, is decoration.
      const decorative = lists[i].some((cls) => {
        const parts = cls.split(":");
        return (
          /^ring(-\d+|-\[[^\]]+\])?$/.test(parts.pop()!) &&
          !parts.some((v) => v.includes("focus"))
        );
      });
      const parsed = list.map((c) => c.parsed);
      for (const { cls, parsed: c } of list) {
        if (c.kind === "bg") continue;
        if (decorative && !c.variants.some((v) => v.includes("focus")))
          continue;
        if (!(c.value in themes.light) || c.alpha === null) {
          problems.push(`${file}: ${cls} has no measurable token and opacity`);
          continue;
        }
        for (const theme of ["light", "dark"] as Theme[]) {
          if (!inTheme(c, theme) || winner(parsed, c, theme, c.kind) !== c) {
            continue;
          }
          const at = `${file}: ${cls} in ${theme}`;
          const fills = parsedLists.flatMap((l) => {
            const p = l.map((x) => x.parsed);
            return p
              .filter((f) => f.kind === "bg" && inTheme(f, theme))
              .map((f) => winner(p, f, theme, "bg"))
              .filter((f) => themes[theme][f.value] === themes[theme][c.value]);
          });
          if (c.alpha === 1) {
            if (fills.length) {
              problems.push(`${at}: flush against a fill of its own colour`);
            }
            continue;
          }
          const halo = halos[theme].find(
            (h) => h.token === c.value && h.alpha === c.alpha,
          );
          if (!halo) {
            problems.push(`${at}: unmeasured halo`);
            continue;
          }
          const mine = seen.get(halo) ?? new Set<number>();
          seen.set(halo, mine);
          for (const f of fills) {
            mine.add(f.alpha ?? -1);
            if (!halo.fills.includes(f.alpha ?? -1)) {
              problems.push(`${at}: unmeasured fill /${pct(f.alpha ?? -1)}`);
            }
          }
        }
      }
    }
  }
  for (const theme of ["light", "dark"] as Theme[]) {
    for (const halo of halos[theme]) {
      for (const fill of halo.fills) {
        if (!seen.get(halo)?.has(fill)) {
          problems.push(
            `${theme}: ${halo.token}/${pct(halo.alpha)} lists fill /${pct(fill)}, which nothing draws`,
          );
        }
      }
    }
  }
  return problems;
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
        .filter((m) => m[1] in themes.light || m[1] in literalColors)
        .map((m) => `${m[1]}/${m[2]}`),
    );
    const listed = new Set(
      Object.values(pairs)
        .flat()
        .flatMap(([fg]) =>
          typeof fg === "string"
            ? []
            : [`${fg.token}/${Math.round(fg.alpha * 100)}`],
        ),
    );
    expect([...used].filter((u) => !listed.has(u))).toEqual([]);
  });

  it("holds focus halos at 3:1 on every surface, apart from their own fill", () => {
    const failing = Object.entries(halos).flatMap(([theme, list]) =>
      list.flatMap(({ token, alpha, fills }) =>
        plainSurfaces.flatMap((over) => {
          const tokens = themes[theme as Theme];
          const halo = resolve(tokens, { token, alpha, over });
          const at = `${theme}: ${token}/${pct(alpha)} over ${over}`;
          const surface = contrast(halo, resolve(tokens, over));
          return [
            ...(surface < 3 ? [`${at} (${surface.toFixed(2)})`] : []),
            ...fills.flatMap((fill) => {
              const apart = contrast(
                halo,
                resolve(tokens, { token, alpha: fill, over }),
              );
              // 1.5 is about --border-strong on card: the faintest edge the palette draws.
              return apart < 1.5
                ? [`${at} vs fill /${pct(fill)} (${apart.toFixed(2)})`]
                : [];
            }),
          ];
        }),
      ),
    );
    expect(failing).toEqual([]);
  });

  it("measures every focus ring and the fills beside it, as each theme renders them", () => {
    expect(focusRingProblems(classSources())).toEqual([]);
  });

  it("fills the unchecked Switch track at full strength", () => {
    expect(
      sources((f) => f.endsWith(".tsx")).flatMap((text) => [
        ...text.matchAll(/data-\[state=unchecked\]:bg-input\/\d+/g),
      ]),
    ).toEqual([]);
  });
});
