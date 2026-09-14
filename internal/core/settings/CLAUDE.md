# Settings and secrets (`internal/core/settings`)

Config precedence and the rule that keeps a stored secret bound to the host it
was saved for. The HTTP-side encoding rules are in
[`../../server/CLAUDE.md`](../../server/CLAUDE.md).

- **Config precedence: env (or a dev `.env`) → DB `settings` overrides → defaults.**
  Integrations (qBit/indexer/library) are editable live via the Settings UI.
  `internal/core/settings.Service` persists the change, rebuilds the client, and
  swaps it into `internal/core/clients.Registry`. Handlers and the importer read
  through the registry, so edits apply without a restart.

### A stored secret only ever goes to the host it was saved for (#259)

- **The bug (#259): a stored secret was sent to a caller-supplied host.** Three
  `Test*` and three `Update*` paths fill a blank secret field from storage.
  Filling from storage is what lets the Settings **Test** button work without
  retyping a password the read path deliberately redacts. The same paths then
  connect to the URL the *caller* supplied, so the substitution handed the secret
  to whatever host a request named.
- **`inheritSecret` is the single way a stored secret is read back.** It returns
  an error instead of substituting when the destination differs.
- **The comparison is scheme + host + port**, because the host is what receives
  the secret. A path edit (eg. a different Jackett indexer on the same Jackett)
  must not cost a retype, and a default port written out explicitly is not a
  move.
- **An empty destination inherits the secret instead of returning an error.**
  Nothing is ever connected to, so clearing a URL to disable an integration keeps
  the secret instead of wiping it or 422-ing.
- **ntfy applies its defaults before the comparison, never after.** A blank
  server means the public ntfy.sh. Defaulting afterwards makes it count as "no
  destination", which takes the empty-destination branch and sends a custom
  server's token to ntfy.sh. Moving the default after the comparison is a live
  mutation, which is why `TestBlankNtfyServerDoesNotInheritACustomServersToken`
  exists.
- **The guard is per-request, so a request it lets through must not move the
  baseline the next one is compared against.** ntfy is the one integration whose
  disable signal (topic) is a *different field* from its destination (server). A
  blank-topic save persisting the caller's server would rebind the inherited
  token to it, and the follow-up request would match. Two ordinary requests would
  then reinstate the stored-secret leak (#259).
- **`UpdateNotify` therefore keeps the stored server on the blank-topic branch**,
  and only while a stored token is being reused, so staging a server before
  choosing a topic still saves.
- **Download and indexer cannot have the moving-baseline shape.** Their disable
  signal *is* the destination, so clearing the URL stores an empty one, and
  `sameDestination`'s hostless fallback rejects every later host.
- **The save paths apply the rule too, not only the tests.** A save rebuilds the
  live client against the new URL, and it authenticates on the next poll. Fixing
  only the tests would leave the same exfiltration one `PUT` away.
- **The host guard is deliberately not an access-control fix.** The cross-origin hole that
  makes it reachable without a credential is the missing CSRF defence in `local`
  auth mode (#269). In `enabled` auth required-mode the caller is authenticated
  anyway.
- **What the host guard protects is the secret *leaving* the app** (an indexer key is a
  private-tracker account credential, a qBit password is often reused). The guard also
  protects the coherence of the `GET /settings` redaction, which exists so that
  API access does not reveal the secrets.
