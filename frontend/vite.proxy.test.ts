import { describe, expect, it } from "vitest";

import config from "./vite.config.ts";
import {
  apiProxyOptions,
  apiProxyTarget,
  DEFAULT_API_TARGET,
} from "./vite.proxy.ts";

describe("apiProxyTarget", () => {
  it("falls back to the historical target when nothing is set", () => {
    expect(apiProxyTarget(undefined)).toBe(DEFAULT_API_TARGET);
    expect(apiProxyTarget("")).toBe(DEFAULT_API_TARGET);
    expect(apiProxyTarget("   ")).toBe(DEFAULT_API_TARGET);
  });

  it("rejects a value with no colon rather than mis-splitting it", () => {
    expect(apiProxyTarget("9797")).toBe(DEFAULT_API_TARGET);
    expect(apiProxyTarget("localhost")).toBe(DEFAULT_API_TARGET);
  });

  it("reads a bare port form, which is what TRANSPONDARR_ADDR usually holds", () => {
    expect(apiProxyTarget(":9898")).toBe("http://localhost:9898");
  });

  // A wildcard bind is not an address a browser can dial, so the host has to be
  // replaced rather than passed through. The port differs from the default's
  // deliberately: on 9797 the rewrite and the fallback produce the same string,
  // so the assertion would pass for either.
  it("dials localhost for a wildcard bind, keeping the port", () => {
    expect(apiProxyTarget("0.0.0.0:9898")).toBe("http://localhost:9898");
    expect(apiProxyTarget("[::]:9898")).toBe("http://localhost:9898");
  });

  it("keeps an explicit host", () => {
    expect(apiProxyTarget("127.0.0.1:1234")).toBe("http://127.0.0.1:1234");
  });

  it("passes a full URL through", () => {
    expect(apiProxyTarget("http://box.local:9797")).toBe(
      "http://box.local:9797",
    );
  });
});

describe("apiProxyOptions", () => {
  // The shorthand `"/api": target` turns changeOrigin on, which rewrites Host to
  // the API's port. The browser's Origin stays on the dev server's port, so the
  // #269 check reads every write from `npm run dev` as cross-origin and 403s it
  // while reads carry on working.
  it("pins changeOrigin off so dev-server writes are not cross-origin", () => {
    expect(apiProxyOptions(undefined).changeOrigin).toBe(false);
  });

  it("resolves the target the same way", () => {
    expect(apiProxyOptions(":9898").target).toBe(apiProxyTarget(":9898"));
    expect(apiProxyOptions(undefined).target).toBe(DEFAULT_API_TARGET);
  });
});

// Asserting apiProxyOptions alone would leave the shorthand -- which is what a
// later edit reaches for -- reachable from the config, so read the config itself.
describe("the dev server's proxy config", () => {
  it("uses the pinned options rather than the string shorthand", async () => {
    const resolved = await (
      config as (env: {
        mode: string;
        command: "serve";
      }) => Promise<{ server: { proxy: Record<string, unknown> } }>
    )({ mode: "development", command: "serve" });
    expect(resolved.server.proxy["/api"]).toEqual(apiProxyOptions(undefined));
  });
});
