# Security Policy

## Supported versions

Transpondarr is pre-1.0. Security fixes are made against the **latest** release and
`main` only. Pin to a tagged release and update promptly.

## Reporting a vulnerability

Please report vulnerabilities **privately** — do not open a public issue.

Use GitHub's private reporting: the repository's **Security → Report a vulnerability**
tab (GitHub Security Advisories). Include a description, affected version/commit, and
reproduction steps. You'll get an acknowledgement and a fix timeline; coordinated
disclosure is appreciated.

## Security model & deployment notes

Transpondarr is self-hosted software intended to run on a trusted home/LAN network,
typically behind a reverse proxy. Keep these in mind when exposing it:

- **Authentication.** Humans log in (username + argon2id password) and get an httpOnly
  session cookie; machine clients (dashboards, scripts) use the `X-Api-Key` header.
  The API key is full-access and does not expire — treat it as a secret, and rotate
  it from **Settings → API access** if it leaks.

- **`local` auth mode trusts your whole private network.** With
  `TRANSPONDARR_AUTH_REQUIRED=local`, any request from a loopback **or private-range**
  address (`10/8`, `172.16/12`, `192.168/16`, link-local) that carries no proxy
  forwarding headers skips authentication. That is a larger trust boundary than "the
  local host" — every device on your LAN can reach the full API unauthenticated. Use
  it only on networks you trust. Behind a reverse proxy, prefer the default `enabled`
  mode; ensure the proxy sets a standard forwarding header (`X-Forwarded-For`,
  `X-Forwarded-Proto`, etc.) so proxied requests are never treated as local. To
  block DNS-rebinding (a malicious page re-pointing its domain at your private IP so
  a LAN browser's requests appear local), the bypass additionally requires the
  request's `Host` header to be an IP literal or `localhost` — so in `local` mode,
  reaching the UI by a hostname still requires a real login.

- **Run as the data owner, not root.** The container image is distroless and does not
  remap UID/GID; set `user:` to the account that owns your media volume.

## Known limitations (deferred hardening)

- **Server-side request scope (SSRF).** The indexer, qBittorrent, and release download
  URLs are operator-configured and fetched server-side without a host/IP allowlist. The
  risk is bounded to an authenticated administrator configuring those endpoints; an
  allowlist is planned. Do not expose the configuration surface to untrusted users.
