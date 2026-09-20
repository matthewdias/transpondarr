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

- **`local` auth mode skips login for your whole private network.** With
  `TRANSPONDARR_AUTH_REQUIRED=local`, any request from a loopback **or private-range**
  address (`10/8`, `172.16/12`, `192.168/16`, link-local) that has no proxy
  forwarding headers skips authentication. So the trust boundary is larger than "the
  local host": every device on your LAN can reach the full API unauthenticated. Use
  `local` auth mode only on networks you trust. Behind a reverse proxy, prefer the
  default `enabled` auth mode, and make sure the proxy sets a standard forwarding
  header (`X-Forwarded-For`, `X-Forwarded-Proto`, etc.) so proxied requests are never
  treated as local.

  To block DNS rebinding, the bypass also requires the request's `Host` header to be
  an IP literal or `localhost`. In DNS rebinding, a malicious page re-points its
  domain at your private IP so that a LAN browser's requests appear local. So in
  `local` auth mode, reaching the UI by a hostname still requires a real login. The
  `Host` check stops *rebinding* but not *cross-origin requests*. A page on any
  website someone on your LAN opens can address Transpondarr by its IP directly.
  Such a request has an IP literal in `Host` and a private peer address, so the
  bypass applies to it. Read "every device on your LAN" as including "every website
  anyone on your LAN visits".

- **Transpondarr rejects a request that changes something when it comes from another
  website.** Browsers attach an `Origin` header to every request that is not a plain
  read, so a `POST`, `PUT`, `PATCH` or `DELETE` naming an origin other than this
  server's gets a `403`. A request with no `Origin` is allowed, which is what keeps
  `curl`, dashboards and the `X-Api-Key` path working; a browser can't leave the
  header off. The check has two limits. Reads are not checked, so a page on another
  website can still start a search you didn't ask for and use part of the AniList
  request budget, though it changes nothing on disk. And the check compares
  hostnames, so a DNS-rebinding page, which sends requests to Transpondarr under its
  own domain name, passes as same-origin. The `Host` check described above already
  blocks such a page from everything that needs a login. So first-run setup is the
  one thing such a page can still use, and only until you have created the admin
  account.

  Behind a reverse proxy, configure the proxy to forward the address your browser
  uses: set `X-Forwarded-Host`, or leave it unset and pass `Host` through unchanged.
  A part of the address the proxy leaves out is left out of the comparison instead
  of being filled in, so `X-Forwarded-Host` with no port (nginx's `$host`) is
  compared by hostname alone. `X-Forwarded-Port` is deliberately ignored: it contains
  the port the proxy listens on, which differs from the published port whenever a
  container maps ports.

- **Run as the data owner, not root.** The container starts as root only to fix
  ownership of `/config`, then drops to `PUID`/`PGID` (default `1000:1000`) before
  serving. Set those two variables to the UID:GID qBittorrent runs as, which is the
  user that can hardlink its downloads (README explains why, and what to do when
  you need a different one).
  `PUID=0` skips the drop and keeps the server running as root. If you set `user:` (or `--user`) instead, the root phase is skipped and
  `PUID`/`PGID` are ignored, so `/config` must already be writable by that user.
  Docker creates a missing bind-mount directory owned by root, and the server then
  exits at startup with an error naming the uid it runs as.
  With `cap_drop: ALL`, the root phase needs `CHOWN`, `SETUID`, `SETGID` and
  `DAC_READ_SEARCH` added back, as [`docker-compose.yml`](docker-compose.yml) does;
  the drop clears them before serving. `PUID=0` has no drop, so the server keeps
  all four for as long as it runs: remove `cap_add` if you run as root.

  **Running as root costs you hardlinks under this compose file.** The Linux kernel
  setting `fs.protected_hardlinks`, on by default, refuses a hardlink to a file the
  caller neither owns nor can both read and write. Holding `CAP_FOWNER` exempts a
  caller from that check (README explains what it means for imports). `cap_drop: ALL`
  removes `CAP_FOWNER`, so the kernel checks root's ownership like anyone else's and
  the server can't hardlink a download qBittorrent owns. Removing `cap_add` doesn't
  restore it — that leaves no capabilities at all. `auto` import mode copies each
  file instead, at twice the disk space; in `hardlink` import mode the grab row waits
  in the Activity queue with an "operation not permitted" error.

  **Running as root also costs you writes into folders another user owns.**
  `DAC_READ_SEARCH` grants read and search, not write, so an import into a library
  folder qBittorrent's user created fails with "permission denied". Adding
  `cap_add: [FOWNER, DAC_OVERRIDE]` restores that and the hardlinks above, at the
  cost of letting the server past every file-ownership and permission check on the
  mount for as long as it runs. Running as qBittorrent's user needs neither
  capability, which is why it's the recommendation above.

## Known limitations (deferred hardening)

- **Server-side request scope (SSRF).** The indexer, qBittorrent, and release download
  URLs are operator-configured and fetched server-side without a host/IP allowlist. In
  `local` auth mode, anyone who can send this server a request can set those URLs,
  because the peer address is the only credential there. In `enabled` auth mode, only
  an authenticated administrator can. An allowlist is planned. Do not expose the
  configuration surface to untrusted users.
