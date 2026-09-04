package server

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"testing"
)

// writeReq builds the request shape the guard reads: method, path, Host, and
// whatever headers a case sets.
func writeReq(method, host string, headers map[string]string) *http.Request {
	req := &http.Request{
		Method: method,
		URL:    &url.URL{Path: "/api/v1/settings/apikey/regenerate"},
		Host:   host,
		Header: http.Header{},
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

func TestSameOriginWriteIsAllowed(t *testing.T) {
	cases := map[string]*http.Request{
		"ip literal and port": writeReq(http.MethodPost, "192.168.1.10:9797", map[string]string{
			"Origin": "http://192.168.1.10:9797",
		}),
		"localhost": writeReq(http.MethodPost, "localhost:9797", map[string]string{
			"Origin": "http://localhost:9797",
		}),
		// A browser elides a port the scheme implies, so the two spellings of :80
		// have to compare equal or every write from a port-80 install gets a 403.
		"default port elided on one side": writeReq(http.MethodPost, "transpondarr.lan:80", map[string]string{
			"Origin": "http://transpondarr.lan",
		}),
		"case-insensitive host": writeReq(http.MethodPost, "Transpondarr.LAN", map[string]string{
			"Origin": "http://transpondarr.lan",
		}),
	}
	for name, req := range cases {
		if crossOriginWrite(req) {
			t.Errorf("%s: same-origin write refused", name)
		}
	}
}

func TestCrossOriginWriteIsRefused(t *testing.T) {
	cases := map[string]*http.Request{
		"different host": writeReq(http.MethodPost, "192.168.1.10:9797", map[string]string{
			"Origin": "https://evil.example",
		}),
		"different port on the same host": writeReq(http.MethodPost, "192.168.1.10:9797", map[string]string{
			"Origin": "http://192.168.1.10:8080",
		}),
		"host as a prefix of the origin": writeReq(http.MethodPost, "192.168.1.10:9797", map[string]string{
			"Origin": "http://192.168.1.10.evil.example:9797",
		}),
		// Reading the header as text rather than as a URL would take this: the
		// address before the @ is userinfo, and evil.example is the host.
		"our address hidden in userinfo": writeReq(http.MethodPost, "192.168.1.10:9797", map[string]string{
			"Origin": "http://192.168.1.10:9797@evil.example",
		}),
		"PUT": writeReq(http.MethodPut, "192.168.1.10:9797", map[string]string{
			"Origin": "https://evil.example",
		}),
		"PATCH": writeReq(http.MethodPatch, "192.168.1.10:9797", map[string]string{
			"Origin": "https://evil.example",
		}),
		"DELETE": writeReq(http.MethodDelete, "192.168.1.10:9797", map[string]string{
			"Origin": "https://evil.example",
		}),
	}
	for name, req := range cases {
		if !crossOriginWrite(req) {
			t.Errorf("%s: cross-origin write allowed", name)
		}
	}
}

// TestAbsentOriginIsAllowed pins the deliberate fail-open: curl, a dashboard and
// the API-key path send no Origin, and a browser cannot omit one on a cross-site
// write.
func TestAbsentOriginIsAllowed(t *testing.T) {
	req := writeReq(http.MethodPost, "192.168.1.10:9797", nil)
	if crossOriginWrite(req) {
		t.Fatal("write with no Origin refused")
	}
}

// TestNullOriginIsRefused checks the https-attacker-page shape: a page on an https
// origin posting to a plain-http target sends "null" rather than its own origin, so
// treating that as absent would admit the likeliest setup.
func TestNullOriginIsRefused(t *testing.T) {
	req := writeReq(http.MethodPost, "192.168.1.10:9797", map[string]string{"Origin": "null"})
	if !crossOriginWrite(req) {
		t.Fatal("Origin: null allowed")
	}
}

func TestForwardedHeadersNameTheExpectedOrigin(t *testing.T) {
	// A proxy that rewrites Host to the upstream still names the public origin.
	proxied := writeReq(http.MethodPost, "127.0.0.1:9797", map[string]string{
		"Origin":            "https://transpondarr.example",
		"X-Forwarded-Host":  "transpondarr.example",
		"X-Forwarded-Proto": "https",
	})
	if crossOriginWrite(proxied) {
		t.Error("same-origin write behind a proxy refused")
	}

	hostile := writeReq(http.MethodPost, "127.0.0.1:9797", map[string]string{
		"Origin":            "https://evil.example",
		"X-Forwarded-Host":  "transpondarr.example",
		"X-Forwarded-Proto": "https",
	})
	if !crossOriginWrite(hostile) {
		t.Error("cross-origin write behind a proxy allowed")
	}
}

// TestProxiedWithoutForwardedHostIsAllowed pins the second fail-open: a proxy that
// rewrites Host without sending X-Forwarded-Host leaves us unable to determine the
// public origin, and a 403 there would stop that install writing anything. The attack
// this checks for has no forwarding header, because a cross-site page can't set one
// without a preflight. We give up the check on a proxied install, which is
// authenticated anyway -- a forwarding header disables the local-address bypass, so a
// session cookie is the only way in and SameSite=Lax already excludes it cross-site.
func TestProxiedWithoutForwardedHostIsAllowed(t *testing.T) {
	for _, header := range []string{"X-Forwarded-For", "X-Real-IP", "X-Forwarded-Proto", "Via"} {
		req := writeReq(http.MethodPost, "127.0.0.1:9797", map[string]string{
			"Origin": "https://evil.example",
			header:   "203.0.113.9",
		})
		if crossOriginWrite(req) {
			t.Errorf("%s: proxied write with no X-Forwarded-Host refused", header)
		}
	}
}

// TestUnstatedPortIsNotCompared checks nginx's own idiom: `X-Forwarded-Host $host`
// excludes the port ($http_host is the spelling that keeps it), so an install
// published on a non-default port would otherwise 403 every write from its own UI.
func TestUnstatedPortIsNotCompared(t *testing.T) {
	req := writeReq(http.MethodPost, "127.0.0.1:9797", map[string]string{
		"Origin":            "https://transpondarr.example:8443",
		"X-Forwarded-Host":  "transpondarr.example",
		"X-Forwarded-Proto": "https",
	})
	if crossOriginWrite(req) {
		t.Error("portless X-Forwarded-Host refused a write from the install's own UI")
	}

	// The host still has to match, so dropping the port costs only the port.
	req.Header.Set("Origin", "https://evil.example:8443")
	if !crossOriginWrite(req) {
		t.Error("cross-origin write allowed once the port went unstated")
	}

	// X-Forwarded-Port states it, and then it is compared again.
	req.Header.Set("Origin", "https://transpondarr.example:8443")
	req.Header.Set("X-Forwarded-Port", "9443")
	if !crossOriginWrite(req) {
		t.Error("port mismatch allowed when X-Forwarded-Port stated one")
	}
	req.Header.Set("X-Forwarded-Port", "8443")
	if crossOriginWrite(req) {
		t.Error("matching X-Forwarded-Port refused")
	}
}

// TestUnstatedSchemeIsNotCompared checks a TLS-terminating proxy that passes Host
// through and doesn't send X-Forwarded-Proto: the request arrives over plain http
// while the browser sends an https Origin, so comparing a scheme nothing stated would
// 403 every write from that install's own UI.
func TestUnstatedSchemeIsNotCompared(t *testing.T) {
	req := writeReq(http.MethodPost, "transpondarr.example", map[string]string{
		"Origin": "https://transpondarr.example",
	})
	if crossOriginWrite(req) {
		t.Fatal("https Origin refused against an unstated scheme")
	}

	// A stated scheme is still compared. The proxy has to name the host too, or
	// the public origin is unknowable and nothing is compared at all.
	req = writeReq(http.MethodPost, "127.0.0.1:9797", map[string]string{
		"Origin":            "http://transpondarr.example",
		"X-Forwarded-Host":  "transpondarr.example",
		"X-Forwarded-Proto": "https",
	})
	if !crossOriginWrite(req) {
		t.Fatal("scheme mismatch allowed when the proxy stated https")
	}

	// Same, when the connection itself is TLS.
	req = writeReq(http.MethodPost, "transpondarr.example", map[string]string{
		"Origin": "http://transpondarr.example",
	})
	req.TLS = &tls.ConnectionState{}
	if !crossOriginWrite(req) {
		t.Fatal("scheme mismatch allowed over TLS")
	}
}

// TestSafeMethodsAreNotGuarded states the accepted residual risk as an assertion: a
// cross-site GET still runs its handler, so the four reads that make outbound calls
// still work.
func TestSafeMethodsAreNotGuarded(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		req := writeReq(method, "192.168.1.10:9797", map[string]string{"Origin": "https://evil.example"})
		if crossOriginWrite(req) {
			t.Errorf("%s treated as a write", method)
		}
	}
}

// TestNonAPIPathsAreNotGuarded keeps the check scoped to the API, matching
// requiresAuth: the embedded SPA is static files and changes nothing.
func TestNonAPIPathsAreNotGuarded(t *testing.T) {
	req := writeReq(http.MethodPost, "192.168.1.10:9797", map[string]string{"Origin": "https://evil.example"})
	req.URL = &url.URL{Path: "/index.html"}
	if crossOriginWrite(req) {
		t.Fatal("non-API path guarded")
	}
}
