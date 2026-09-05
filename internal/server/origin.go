package server

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// writeMethods are the ones that change something. GET and HEAD are absent
// deliberately: we accept a cross-site read as a residual risk (#269).
var writeMethods = []string{
	http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
}

// crossOriginGuard returns 403 for an API write sent by another website. In "local"
// auth mode the peer address is the only credential, so this is the only check.
func crossOriginGuard() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if crossOriginWrite(req) {
				writeForbidden(w, "cross-origin request refused")
				return
			}
			next.ServeHTTP(w, req)
		})
	}
}

// crossOriginWrite reports whether another website sent this write. It reads Origin,
// not Sec-Fetch-*, which browsers don't send to a private address (see SECURITY.md).
func crossOriginWrite(req *http.Request) bool {
	if !strings.HasPrefix(req.URL.Path, "/api/") || !slices.Contains(writeMethods, req.Method) {
		return false
	}
	raw := req.Header.Get("Origin")
	if raw == "" {
		// Machine clients don't send one, and a browser can't omit one cross-site.
		return false
	}
	got, ok := parseOrigin(raw)
	if !ok {
		// An https page posting to an http target sends "null", not its own origin.
		return true
	}
	want, ok := expectedOrigin(req)
	if !ok {
		return false
	}
	if got.host != want.host {
		return true
	}
	// If nothing stated the port, don't compare it: a proxy that doesn't send one
	// would otherwise 403 that install's own UI.
	if want.portStated && got.port != want.port {
		return true
	}
	return want.scheme != "" && got.scheme != want.scheme
}

// origin is what we compare. An empty scheme, or portStated false, means the request
// didn't state that part, so we leave it out.
type origin struct {
	scheme     string
	host       string
	port       string
	portStated bool
}

// expectedOrigin reports the origin the client addressed, and whether we can determine
// it. Trusting X-Forwarded-Host is safe: setting one needs a preflight.
func expectedOrigin(req *http.Request) (origin, bool) {
	host, portStated := firstValue(req.Header.Get("X-Forwarded-Host")), true
	if host == "" {
		host = req.Host
	} else if _, _, err := net.SplitHostPort(host); err != nil {
		// nginx's $host excludes the port ($http_host keeps it). X-Forwarded-Port
		// can't fill it in: it names the port the proxy listens on, which differs
		// from the published one whenever a container maps ports.
		portStated = false
	}
	// The scheme-relative spelling, so an unstated scheme still parses.
	spelling := "//" + host
	if scheme := statedScheme(req); scheme != "" {
		spelling = scheme + "://" + host
	}
	want, ok := parseOrigin(spelling)
	want.portStated = portStated
	return want, ok
}

// statedScheme is the scheme the client used, empty when nothing states one: a proxy
// that terminates TLS without X-Forwarded-Proto forwards the request over plain http.
func statedScheme(req *http.Request) string {
	// Only the two this server can be reached over, so a header holding anything
	// else reads as unstated rather than building a spelling that won't parse,
	// which would switch the whole check off.
	switch p := strings.ToLower(firstValue(req.Header.Get("X-Forwarded-Proto"))); p {
	case "http", "https":
		return p
	}
	if req.TLS != nil {
		return "https"
	}
	return ""
}

// firstValue takes the original value from a forwarding header, which each proxy in a
// chain appends its own to.
func firstValue(header string) string {
	v, _, _ := strings.Cut(header, ",")
	return strings.TrimSpace(v)
}

// parseOrigin reduces an origin to its comparable parts, as settings.destination does
// (#259). A value with no host ("null", say) returns false rather than an empty one.
func parseOrigin(raw string) (origin, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return origin{}, false
	}
	scheme := strings.ToLower(u.Scheme)
	port := u.Port()
	if impliedPort(scheme, port) {
		port = ""
	}
	return origin{
		scheme:     scheme,
		host:       strings.ToLower(u.Hostname()),
		port:       port,
		portStated: true,
	}, true
}

// impliedPort reports whether writing a port out gives the same address as leaving it
// off. An unstated scheme takes both, so we don't 403 over how a port was written.
func impliedPort(scheme, port string) bool {
	switch scheme {
	case "http":
		return port == "80"
	case "https":
		return port == "443"
	case "":
		return port == "80" || port == "443"
	}
	return false
}

// writeForbidden sends problem+json, not http.Error's text/plain: the SPA reads
// `detail`, and a plain body reaches the operator as "HTTP 403" with no reason.
func writeForbidden(w http.ResponseWriter, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"title":  "Forbidden",
		"status": http.StatusForbidden,
		"detail": detail,
	})
}
