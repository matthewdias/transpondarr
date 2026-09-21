/// <reference types="node" />
import { readdirSync, readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const src = new URL("../", import.meta.url);

// Read from disk: vitest blanks CSS imports, `?raw` included.
const css = readFileSync(new URL("index.css", src), "utf8");

const query = "@media (prefers-reduced-motion: reduce)";

// Brace-matched, so a nested rule inside the query is included too.
function block(): string {
  const at = css.indexOf(query);
  if (at === -1) throw new Error(`index.css has no ${query} block`);
  let depth = 0;
  for (let i = css.indexOf("{", at); i < css.length; i++) {
    if (css[i] === "{") depth++;
    else if (css[i] === "}" && --depth === 0) return css.slice(at, i + 1);
  }
  throw new Error(`index.css leaves the ${query} block unclosed`);
}

// tw-animate-css registers one custom property per movement channel of its
// enter/exit keyframes, so the dependency defines what an overlay can animate.
function movementChannels(): string[] {
  const dist = readFileSync(
    new URL("../node_modules/tw-animate-css/dist/tw-animate.css", src),
    "utf8",
  );
  return [
    ...new Set(
      [...dist.matchAll(/@property\s+(--tw-(?:enter|exit)-[\w-]+)/g)].map(
        (m) => m[1],
      ),
    ),
  ].filter((channel) => !channel.endsWith("-opacity"));
}

// "file: call" -> why a media query cannot switch that motion off.
const allowed: Record<string, string> = {};

// Motion a media query cannot switch off: the Web Animations API, and the two
// ways to request a smooth scroll.
const scriptedMotion =
  /\.animate\(|scrollIntoView\(|behavior:\s*["']smooth["']|scroll-smooth/g;

function sources(): string[] {
  return readdirSync(src, { recursive: true, encoding: "utf8" }).filter(
    (f) => /\.tsx?$/.test(f) && !/\.test\.tsx?$/.test(f),
  );
}

describe("reduced motion", () => {
  it("resets every movement channel tw-animate-css defines", () => {
    const reset = new Set(
      [...block().matchAll(/(--tw-(?:enter|exit)-[\w-]+):\s*initial/g)].map(
        (m) => m[1],
      ),
    );
    expect(movementChannels().filter((c) => !reset.has(c))).toEqual([]);
  });

  it("leaves the opacity channels alone, so an overlay still fades", () => {
    expect(block()).not.toMatch(/--tw-(?:enter|exit)-opacity/);
  });

  it("keeps loading spinners turning", () => {
    expect(block().match(/animation[\w-]*\s*:[^;]*/g)).toBeNull();
  });

  it("collapses a transition past an inline style", () => {
    const collapsed = block().match(
      /transition-duration:\s*([\d.]+)(m?s)\s*!important/,
    );
    expect(
      collapsed,
      `${query} sets no !important transition-duration`,
    ).not.toBeNull();
    const ms = Number(collapsed![1]) * (collapsed![2] === "s" ? 1000 : 1);
    expect(ms).toBeLessThan(1);
  });

  it("drives no motion from script, where the query cannot reach it", () => {
    const sites = sources().flatMap((file) =>
      readFileSync(new URL(file, src), "utf8")
        .split("\n")
        .flatMap((line, i) =>
          [...line.matchAll(scriptedMotion)]
            .filter((m) => !(`${file}: ${m[0]}` in allowed))
            .map((m) => `${file}:${i + 1} ${m[0]}`),
        ),
    );
    expect(sites).toEqual([]);
  });
});
