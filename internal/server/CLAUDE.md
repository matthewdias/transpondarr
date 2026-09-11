# HTTP surface (`internal/server`)

Route layout, the cross-origin write guard, and how a settings body encodes
"leave this alone" versus "set this to the default". Secret handling on the
service side is in [`../core/settings/CLAUDE.md`](../core/settings/CLAUDE.md).

- **We reject a write whose `Origin` names another origin, and `Origin` is the only
  header that can show that (#269).** `local` auth mode authorizes on peer address
  alone. A hostile page
  addresses the server by its IP, so `Host` is an IP literal and `RemoteAddr` is
  private — the same shape `rebinding_test.go` names the legitimate LAN case. The
  rebinding check is correct and tests a different condition. `crossOriginGuard`
  therefore runs ahead of `authMiddleware`, so one check applies to the Huma routes
  and the hand-rolled auth ones alike. The hand-rolled ones matter most: `decodeJSON`
  ignores `Content-Type`, so `/auth/setup` accepts a `text/plain` body, and its only
  other check is `Configured()`. In `local` auth mode `Configured()` stays false,
  because `auth-gate.tsx` renders the setup screen only when `!authenticated` and a
  LAN browser always is. Nobody creates the admin account, so a hostile page can
  create it with a password it chose — which is why we don't scope the check to
  `local` auth mode.
  **`Sec-Fetch-Site` can't do this job, and choosing it is the trap.** A browser
  appends those headers only to potentially trustworthy URLs, and `192.168/16` is not
  one, so on the deployment this is about they never arrive. Such a check would still
  pass its own mutation test, because `httptest` listens on `127.0.0.1`. `Origin` has
  no trustworthiness condition and browsers append it to every request but `GET` and
  `HEAD`. So it discriminates all three shapes a browser sends without a preflight:
  bodyless, `text/plain`, and a JSON body with the header omitted — the last being
  the one content type Huma accepts by default.
  Three constants. **Absent means allowed**, which is what keeps `curl`,
  dashboards and the API key working, and is safe because a browser can't omit
  `Origin` on a cross-site write. **`null` means rejected**, since an https page
  posting to an http target sends that rather than its own origin; treating the two
  alike would admit the likeliest attacker setup. And **we don't compare a part
  nobody stated**, which never applies to the host but applies to the scheme and
  the port. A proxy that terminates TLS without setting `X-Forwarded-Proto`
  forwards over plain http against an https `Origin`. Separately, nginx's
  `X-Forwarded-Host $host` excludes the port (`$http_host` is the spelling that
  keeps it), so an install published on `:8443` gives a portless expected host
  against an `Origin` that includes `:8443`. Comparing either would 403 that
  install's own UI. **`X-Forwarded-Port` does not fill that in**: nginx's
  `$server_port` and a Traefik entrypoint both name the port the proxy listens on,
  which differs from the published one whenever a container maps ports.
  **What we do not do is fail open when a proxy names no host**, which an earlier
  round of this change did, on the reasoning that a proxied install is
  authenticated anyway. `requiresAuth` exempts `/auth/setup`, `/auth/login` and
  `/auth/logout` in every auth required-mode, so that reasoning was false exactly
  where it mattered. cloudflared, Tailscale Serve and any nginx setting only `For`
  and `Proto` send a forwarding header and no `X-Forwarded-Host`, and a hostile
  page got the admin account on a fresh `enabled` install. `Host` is the fallback,
  because a proxy that forwards anything usually forwards `Host` unchanged too, and
  one that rewrites it gets a diagnosable 403 rather than a silent hole.
  `statedScheme` takes only `http` and `https` for the same class of reason: any
  other value built a spelling `url.Parse` rejects, and an unparseable expected
  origin allowed everything.
  The 403 is **problem+json, not `http.Error`'s text/plain**, because
  `throwApiError` reads `detail` and from a plain body the operator gets
  "HTTP 403" and no cause — the string the upgrade note names. Reads stay unchecked, a
  stated residual risk: four `GET`s make outbound
  calls and use the AniList request budget. A rebinding page is *same-origin* by
  construction, so this check does not apply to one. `isLocalRequest` rejects it
  on every route that needs a login, which leaves the pre-setup window, named in SECURITY.md.
  **`apiProxyOptions` pins the Vite dev proxy's `changeOrigin` off** for the same
  reason the port rule exists. The string shorthand turns it on, which rewrites
  `Host` to the API's port and adds no forwarding header, so `make dev` plus
  `npm run dev` would 403 every write while reads kept working. That is why
  `apiProxyTarget` (a string) is wrapped rather than changed: a later edit reverts
  to the shorthand. It costs one thing: `npm run dev -- --host` reached by hostname
  now forwards that hostname, which `isLocalRequest` rejects, so `local` auth mode
  needs a login there.
- **Auth is forms-based** (`internal/core/auth`): the web UI logs in (username +
  argon2id password) and gets an httpOnly session cookie; the **API key** is for
  machine clients only (`X-Api-Key`). A request to `/api/*` is authorized by a
  valid session cookie, a valid API key, or — in `local` required-mode — a
  loopback/private request with no forwarding headers. The key is resolved as
  `TRANSPONDARR_API_KEY` env → DB-persisted → generate-and-persist (`resolveAPIKey`
  in `cmd/transpondarrd`); it persists across restarts.
- **A settings body is its section's whole state, so `omitempty` is an argument
  rather than a default (#227).** A field the service would fill in with a
  default is required — the library mode and layout, the qBit category, the
  stall hours, the ntfy server, every notify toggle — because omitting it
  *selects* that default instead of leaving it alone, invisibly to the sender:
  #129's flat library reverted to season folders on a save that never mentioned
  the layout, and the DB row then outranked the env var permanently. A field is
  `omitempty` only where absent and empty are the same instruction — a blank
  secret keeps the stored one, a blank URL, root or topic switches that piece
  off. Sending a required field empty still takes the default, and that *is* the
  distinction: the client said so — **except where the field has an `enum`
  tag**, which rejects an empty value outright, so `mode`, `series_layout` and
  automation's `mode` are 422 either way and the handler's `ValidImportMode` /
  `ValidSeriesLayout` guards are unreachable defence in depth. The rule is about
  the encoding, not about Huma, so it applies to the hand-rolled bodies too —
  `POST /api/v1/auth/mode` validates the mode itself, where the service would
  otherwise read an absent one as `enabled` and lock a `local` install out. It
  validates *exactly*, matching those enums: `auth.ValidRequired` rejects a case
  variant that `normalizeRequired` would have accepted, because that one reads
  what a stored value or an env var may contain, not what a client sent.
  `TestSettingsInputsRequireEveryDefaultedField` is the audit in runnable form:
  a field moved back to `omitempty` fails it unless someone also takes it out of
  the table, which is where the argument has to be made.
  **The quality-profile body takes the same rule, and being one body for create
  and update is why it has to.** A create that omitted a field did not take the
  column's default — `CreateQualityProfile` writes every column explicitly, so
  it wrote the *zero* over `resolution_order`'s three resolutions and
  `upgrade_v2_above_cutoff`'s on, which is the opposite of what both the schema
  and the editor present as a new profile's starting point. So the usual "POST
  defaults what it omits" idiom was never true here, and splitting create from
  update would have meant inventing those defaults in Go to match the ones SQLite
  already states. What stays `omitempty` is where empty is the value: no
  preference, no excludes, no ranked groups. `blocked` on a group row is
  required for the plain reason — under `omitempty` no client can say "not
  blocked", which this repo's own Go and TypeScript test fixtures both
  demonstrated by being unable to.
- **Route handlers: group by resource; use a receiver when it earns its keep.**
  Each resource gets a `*_routes.go` file with a `register<Resource>Routes(api,
deps)` function; `registerRoutes` in `internal/server/routes.go` is the manifest.
  Multi-route resources that share deps/helpers (titles, settings) hang handlers
  off a per-resource receiver struct (`titleHandler`) built via
  `new<Resource>Handler(deps)`, with shared logic as methods (e.g. `requireTitle`,
  `respond`); single-route groups (system, download, metadata, indexer) keep
  inline closures. The receiver earns its keep around 3+ routes or shared
  helpers/state. Handlers stay thin — push business logic into `internal/core`.
  Auth endpoints are plain-chi, not Huma.
