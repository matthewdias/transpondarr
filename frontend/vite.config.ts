import path from "node:path";
import { defaultExclude, defineConfig } from "vitest/config";
import { loadEnv } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

import { apiProxyOptions } from "./vite.proxy.ts";

// Local-day logic (src/lib/calendar) is only testable against a known zone, and
// New York exercises the interesting case: a UTC evening instant belongs to the
// previous local day. Pinned here rather than per-file because assigning TZ from
// inside a worker is ignored by the threads pool, which surfaces as a one-hour
// arithmetic error rather than as a config problem.
if (process.env.VITEST) process.env.TZ = "America/New_York";

// Pure-logic suites run without a DOM or the jest-dom setup, worth ~350ms of
// environment per file. Anything that renders — or drives fetch — stays off it.
const unitTests = [
  "src/lib/calendar.test.ts",
  "src/lib/chart.test.ts",
  "src/lib/format.test.ts",
  "src/lib/queries.test.ts",
  "src/lib/release-focus.test.ts",
  "src/lib/season.test.ts",
  "src/lib/unmet-goals.test.ts",
  "src/pages/settings/sections/profile-editor-state.test.ts",
  "vite.proxy.test.ts",
];

// The frontend builds into ../web/dist, which the Go binary embeds (see web/web.go).
// public/.gitkeep rides along into web/dist so emptyOutDir can't drop the committed keeper.
// In dev, `npm run dev` proxies /api to the running transpondarrd server.
// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  // The repo root, not frontend/: this is the same .env / .env.local pair the Go
  // side reads, so one file configures both halves of a worktree.
  const env = loadEnv(mode, path.resolve(__dirname, ".."), "TRANSPONDARR_");
  return {
    plugins: [react(), tailwindcss()],
    base: "./",
    resolve: {
      alias: {
        "@": path.resolve(__dirname, "./src"),
      },
    },
    build: {
      outDir: "../web/dist",
      emptyOutDir: true,
    },
    server: {
      proxy: {
        "/api": apiProxyOptions(env.TRANSPONDARR_ADDR),
      },
    },
    test: {
      projects: [
        {
          extends: true,
          test: {
            name: "unit",
            include: unitTests,
            environment: "node",
          },
        },
        {
          extends: true,
          test: {
            name: "dom",
            // Everything not named above: a new suite defaults to the DOM rather
            // than being silently skipped, so the fast lane is the opt-in.
            include: ["src/**/*.test.{ts,tsx}"],
            exclude: [...defaultExclude, ...unitTests],
            // happy-dom (not jsdom): it resolves the relative-URL fetch/Request calls
            // the api client makes against location, so MSW can intercept the real
            // client path.
            environment: "happy-dom",
            setupFiles: ["./src/test/setup.ts"],
          },
        },
      ],
    },
  };
});
