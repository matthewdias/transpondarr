# HTTP surface (`internal/server`)

Route layout, the cross-origin write guard, and how a settings body encodes
"leave this alone" versus "set this to the default". Secret handling on the
service side is in [`../core/settings/CLAUDE.md`](../core/settings/CLAUDE.md).

## Cross-origin write guard

### Why the guard exists

- **We reject a write whose `Origin` names another origin.** That closes the
  `local` auth mode CSRF hole, where a hostile page a LAN browser visits could
  drive the API (#269). `Origin` is the only header that can show a write is
  cross-origin.
- **Peer address and `Host` can't tell a hostile page from a LAN client.** `local`
  auth mode authorizes on peer address alone. A hostile page addresses the server
  by its IP, so `Host` is an IP literal and `RemoteAddr` is private.
  `rebinding_test.go` names that same shape the legitimate LAN case. The rebinding
  check is correct and tests a different condition.
- **`crossOriginGuard` runs ahead of `authMiddleware`**, so one check applies to
  the Huma routes and the hand-rolled auth routes alike. The hand-rolled ones
  matter most. `decodeJSON` ignores `Content-Type`, so `/auth/setup` accepts a
  `text/plain` body, and its only other check is `Configured()`.
- **In `local` auth mode nobody creates the admin account.** `Configured()` stays
  false, because `auth-gate.tsx` renders the setup screen only when
  `!authenticated` and a LAN browser always is authenticated. So a hostile page
  can create the admin account with a password it chose, which is why we don't
  scope the check to `local` auth mode.

### Why `Origin` and not `Sec-Fetch-Site`

- **`Sec-Fetch-Site` can't do this job, and choosing it is the trap.** A browser
  appends the `Sec-Fetch-*` headers only to potentially trustworthy URLs, and
  `192.168/16` is not one. So on the LAN deployment this guard is about, they
  never arrive. A `Sec-Fetch-Site` check would still pass its own mutation test,
  because `httptest` listens on `127.0.0.1`.
- **`Origin` has no trustworthiness condition**, and browsers append it to every
  request but `GET` and `HEAD`. So it discriminates all three shapes a browser
  sends without a preflight: bodyless, `text/plain`, and a JSON body with the
  header omitted. The last is the one content type Huma accepts by default.

### How `Origin` is compared

- **An absent `Origin` is allowed.** Allowing it keeps `curl`, dashboards and the
  API key working. It is safe because a browser can't omit `Origin` on a
  cross-site write.
- **`null` is rejected.** An https page posting to an http target sends `null`
  instead of its own origin, so treating the two alike would admit the likeliest
  attacker setup.
- **We don't compare a part nobody stated.** The rule never applies to the host,
  but applies to the scheme and the port, where comparing either would 403 the
  install's own UI. For the scheme: a proxy that terminates TLS without setting
  `X-Forwarded-Proto` forwards over plain http against an https `Origin`. For the
  port: nginx's `X-Forwarded-Host $host` excludes the port (`$http_host` is the
  spelling that keeps it). So an install published on `:8443` gives a portless
  expected host against an `Origin` that includes `:8443`.
- **`X-Forwarded-Port` does not fill in the missing port.** nginx's
  `$server_port` and a Traefik entrypoint both name the port the proxy listens
  on. That port differs from the published one whenever a container maps ports.
- **What we do not do is fail open when a proxy names no host.** An earlier round
  of this change did, on the reasoning that a proxied install is authenticated
  anyway. That reasoning was false where it mattered, because `requiresAuth`
  exempts `/auth/setup`, `/auth/login` and `/auth/logout` in every auth
  required-mode. cloudflared, Tailscale Serve and any nginx setting only `For` and
  `Proto` send a forwarding header and no `X-Forwarded-Host`. Behind one of them,
  a hostile page got the admin account on a fresh `enabled` install.
- **`Host` is the fallback when no host is forwarded.** A proxy that forwards
  anything usually forwards `Host` unchanged too. One that rewrites it gets a
  diagnosable 403 rather than a silent hole.
- **`statedScheme` takes only `http` and `https`**, for the same class of reason
  as not failing open. Any other value built a spelling `url.Parse` rejects, and
  an unparseable expected origin allowed everything.

### The 403 and what stays unchecked

- **The 403 is problem+json, not `http.Error`'s text/plain.** `throwApiError`
  reads `detail`, and from a plain body the operator gets "HTTP 403" and no cause.
  "HTTP 403" is the string the upgrade note names.
- **Reads stay unchecked, a stated residual risk.** Four `GET`s make outbound
  calls and use the AniList request budget.
- **A rebinding page is `isLocalRequest`'s to reject, not this guard's.** A
  rebinding page is *same-origin* by construction, so this check does not apply
  to one. `isLocalRequest` rejects it on every route that needs a login, which
  leaves the pre-setup window, named in SECURITY.md.

### The Vite dev proxy

- **`apiProxyOptions` pins the Vite dev proxy's `changeOrigin` off**, for the same
  reason the unstated-port rule exists. The string shorthand turns `changeOrigin`
  on, which rewrites `Host` to the API's port and adds no forwarding header. With
  it on, `make dev` plus `npm run dev` would 403 every write while reads kept
  working.
- **`apiProxyTarget` (a string) is wrapped rather than changed**, because a later
  edit reverts to the shorthand.
- **The cost is a login for `--host` in `local` auth mode.**
  `npm run dev -- --host` reached by hostname now forwards that hostname, which
  `isLocalRequest` rejects. So `local` auth mode needs a login there.

## Auth

- **Auth is forms-based** (`internal/core/auth`). The web UI logs in (username +
  argon2id password) and gets an httpOnly session cookie. The **API key** is for
  machine clients only (`X-Api-Key`).
- **Three things authorize a request to `/api/*`**: a valid session cookie, a
  valid API key, or — in `local` auth required-mode — a loopback/private request
  with no forwarding headers.
- **The API key persists across restarts.** It is resolved as
  `TRANSPONDARR_API_KEY` env → DB-persisted → generate-and-persist
  (`resolveAPIKey` in `cmd/transpondarrd`).

## Settings bodies

### The required-versus-`omitempty` rule

- **A settings body is its section's whole settings state, so `omitempty` is an
  argument rather than a default.** The settings input DTOs were audited against
  this rule in #227.
- **A field the service would fill in with a default is required** (eg. the
  library import mode and layout, the qBit category, the stall hours, the ntfy
  server, every notify toggle). Omitting such a field *selects* that default
  instead of leaving it alone, invisibly to the sender. A flat library, the layout
  option from #129, reverted to season folders on a save that never mentioned the
  layout. The DB row then outranked the env var permanently.
- **A field is `omitempty` only where absent and empty are the same
  instruction** (eg. a blank secret keeps the stored one; a blank URL, root or
  topic switches that piece off).
- **Sending a required field empty still takes the default**, and that *is* the
  distinction from omitting it: the client said so.
- **An `enum` tag rejects an empty value outright.** So the library's `mode`,
  `series_layout` and automation's `mode` are 422 whether empty or omitted. The
  handler's `ValidImportMode` / `ValidSeriesLayout` guards are therefore
  unreachable defence in depth.
- **The rule is about the encoding, not about Huma, so it applies to the
  hand-rolled bodies too.** `POST /api/v1/auth/mode` validates the auth
  required-mode itself. Otherwise the service would read an absent one as
  `enabled` and lock a `local` install out.
- **`POST /api/v1/auth/mode` validates *exactly*, matching those enums.**
  `auth.ValidRequired` rejects a case variant that `normalizeRequired` would have
  accepted. `normalizeRequired` reads what a stored value or an env var may
  contain, not what a client sent.
- **`TestSettingsInputsRequireEveryDefaultedField` is the audit in runnable
  form.** A field moved back to `omitempty` fails it unless someone also takes it
  out of the test's table, which is where the argument has to be made.

### The quality-profile body

- **The quality-profile body takes the same rule, and being one body for create
  and update is why it has to.** A create that omitted a field did not take the
  column's default. `CreateQualityProfile` writes every column explicitly, so it
  wrote the *zero* over `resolution_order`'s three resolutions and
  `upgrade_v2_above_cutoff`'s on. Those zeros are the opposite of what both the
  schema and the editor present as a new profile's starting point.
- **The usual "POST defaults what it omits" idiom was never true here.** Splitting
  create from update would have meant inventing those defaults in Go to match the
  ones SQLite already states.
- **What stays `omitempty` is where empty is the value**: no preference, no
  excludes, no ranked profile groups.
- **`blocked` on a profile group row is required** for the plain reason that
  under `omitempty` no client can say "not blocked". This repo's own Go and
  TypeScript test fixtures both demonstrated it by being unable to.

## Route handlers

- **Group route handlers by resource.** Each resource gets a `*_routes.go` file
  with a `register<Resource>Routes(api, deps)` function. `registerRoutes` in
  `internal/server/routes.go` is the manifest.
- **Use a receiver when it earns its keep**, around 3+ routes or shared helpers or
  handler state. Multi-route resources that share deps or helpers (titles,
  settings) hang handlers off a per-resource receiver struct (`titleHandler`)
  built via `new<Resource>Handler(deps)`. Their shared logic is methods (eg.
  `requireTitle`, `respond`).
- **Single-route groups keep inline closures** (system, download, metadata,
  indexer).
- **Handlers stay thin.** Push business logic into `internal/core`.
- **Auth endpoints are plain-chi, not Huma.**
