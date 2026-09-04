import { describe, expect, it } from "vitest";

import { apiProxyTarget, DEFAULT_API_TARGET } from "./vite.proxy.ts";

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
