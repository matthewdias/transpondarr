/// <reference types="vitest/config" />
import path from "node:path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// The frontend builds into ../web/dist, which the Go binary embeds (see web/web.go).
// public/.gitkeep rides along into web/dist so emptyOutDir can't drop the committed keeper.
// In dev, `npm run dev` proxies /api to the running transpondarrd server.
// https://vite.dev/config/
export default defineConfig({
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
      "/api": "http://localhost:9797",
    },
  },
  test: {
    // happy-dom (not jsdom): it resolves the relative-URL fetch/Request calls the
    // api client makes against location, so MSW can intercept the real client path.
    environment: "happy-dom",
    setupFiles: ["./src/test/setup.ts"],
  },
});
