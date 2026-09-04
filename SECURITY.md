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
  reaching the UI by a hostname still requires a real login. That check stops
  *rebinding*, not *cross-origin*. A page on any website someone on your LAN opens can
  address Transpondarr by its IP directly. Such a request has an IP literal in `Host`
  and a private peer address, so the bypass applies to it. Read "every device on your
  LAN" as including "every website anyone on your LAN visits".

- **Transpondarr rejects a request that changes something when it comes from another
  website.** Browsers attach an `Origin` header to every request that is not a plain
  read, so a `POST`, `PUT`, `PATCH` or `DELETE` naming an origin other than this
  server's gets a `403`. A request with no `Origin` is allowed, which is what keeps
  `curl`, dashboards and the `X-Api-Key` path working; a browser can't leave the
  header off. Two limits are worth knowing. Reads are not checked, so a page on
  another website can still start a search you didn't ask for and use part of the
  AniList request budget, though it changes nothing on disk. And behind a reverse
  proxy that rewrites `Host` without sending `X-Forwarded-Host`, Transpondarr can't
  determine what address the browser used, so it skips the check. Set a standard
  forwarding header — the `local`-mode advice above already asks for one — and the
  check applies again. Whatever the proxy leaves out is left out of the comparison
  rather than guessed at, so an `X-Forwarded-Host` with no port (nginx's `$host`) is
  compared by hostname alone unless `X-Forwarded-Port` gives one.

- **Run as the data owner, not root.** The container image is distroless and does not
  remap UID/GID; set `user:` to the account that owns your media volume.

## Known limitations (deferred hardening)

- **Server-side request scope (SSRF).** The indexer, qBittorrent, and release download
  URLs are operator-configured and fetched server-side without a host/IP allowlist. In
  `local` auth mode that means anyone who can send this server a request, because the
  peer address is the only credential there; otherwise it means an authenticated
  administrator. An allowlist is planned. Do not expose the configuration surface to
  untrusted users.
