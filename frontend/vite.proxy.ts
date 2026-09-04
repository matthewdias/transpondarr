/** What the proxy dialled before the address was configurable. */
export const DEFAULT_API_TARGET = "http://localhost:9797";

/**
 * Resolves the dev proxy's target from a TRANSPONDARR_ADDR-shaped value, so two
 * worktrees can run their servers on different ports at once.
 */
export function apiProxyTarget(addr: string | undefined): string {
  const value = (addr ?? "").trim();
  if (value === "") return DEFAULT_API_TARGET;
  if (value.includes("://")) return value;
  // No colon is not an address: ":9797" and "9797" would otherwise both parse,
  // the second splitting into host "979" and port "9797".
  if (!value.includes(":")) return DEFAULT_API_TARGET;

  const port = value.slice(value.lastIndexOf(":") + 1);
  const host = value.slice(0, value.lastIndexOf(":"));
  if (port === "" || !/^\d+$/.test(port)) return DEFAULT_API_TARGET;
  // A wildcard bind is an address to listen on, never one a browser can dial.
  const dialable =
    host === "" || host === "0.0.0.0" || host === "[::]" ? "localhost" : host;
  return `http://${dialable}:${port}`;
}

/**
 * The dev proxy's options. changeOrigin is pinned off: the string shorthand turns
 * it on, which rewrites Host to the API's port and makes every write from
 * `npm run dev` look cross-origin to the check #269 added.
 */
export function apiProxyOptions(addr: string | undefined) {
  return { target: apiProxyTarget(addr), changeOrigin: false };
}
