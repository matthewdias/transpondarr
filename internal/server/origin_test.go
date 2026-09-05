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

	// With no Host there is no expected origin to compare against, so this is the
	// case that separates rejecting "null" outright from rejecting it because the
	// hosts happened to differ. Without it the branch survives deletion.
	req.Host = ""
	if !crossOriginWrite(req) {
		t.Fatal("Origin: null allowed when the expected origin is unknown")
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

// TestProxiedWithoutForwardedHostComparesHost pins the review's HIGH finding: this
// used to allow the request, on the reasoning that a proxied install is
// authenticated anyway. That is false for the three routes requiresAuth exempts in
// every auth required-mode, so a proxy sending X-Forwarded-For and no
// X-Forwarded-Host handed out /auth/setup. Host is the right fallback: a proxy that
// forwards anything usually forwards Host unchanged too.
func TestProxiedWithoutForwardedHostComparesHost(t *testing.T) {
	for header, value := range map[string]string{
		"X-Forwarded-For":   "203.0.113.9",
		"X-Real-IP":         "203.0.113.9",
		"X-Forwarded-Proto": "https",
		"Via":               "1.1 proxy",
	} {
		hostile := writeReq(http.MethodPost, "transpondarr.example", map[string]string{
			"Origin": "https://evil.example",
			header:   value,
		})
		if !crossOriginWrite(hostile) {
			t.Errorf("%s: proxied cross-origin write allowed", header)
		}

		own := writeReq(http.MethodPost, "transpondarr.example", map[string]string{
			"Origin": "https://transpondarr.example",
			header:   value,
		})
		if crossOriginWrite(own) {
			t.Errorf("%s: proxied write from the install's own UI refused", header)
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

	// X-Forwarded-Port is deliberately not consulted: nginx's $server_port and a
	// Traefik entrypoint both name the port the proxy listens on, which differs
	// from the published one whenever a container maps ports, so reading it would
	// 403 the owner's own UI (the review's MEDIUM finding).
	req.Header.Set("Origin", "https://transpondarr.example:8443")
	req.Header.Set("X-Forwarded-Port", "443")
	if crossOriginWrite(req) {
		t.Error("X-Forwarded-Port refused a write from the install's own UI")
	}
}

// TestUnparseableForwardedProtoDoesNotDisableTheCheck: a header holding something
// that is not a scheme used to build a spelling url.Parse rejects, and an
// unparseable expected origin allows everything. Found by a test of mine that set
// X-Forwarded-Proto to an IP address and passed for the wrong reason.
func TestUnparseableForwardedProtoDoesNotDisableTheCheck(t *testing.T) {
	req := writeReq(http.MethodPost, "transpondarr.example", map[string]string{
		"Origin": "https://evil.example", "X-Forwarded-Proto": "203.0.113.9",
	})
	if !crossOriginWrite(req) {
		t.Fatal("cross-origin write allowed by an unparseable X-Forwarded-Proto")
	}
}

// TestIPv6ForwardedHostIsCompared pins the review's other MEDIUM: an IPv6 literal
// from nginx's $host arrives bracketed, and mishandling it made expectedOrigin
// unparseable, which switched the check off for that install entirely.
func TestIPv6ForwardedHostIsCompared(t *testing.T) {
	// X-Forwarded-Port is set on purpose. It is ignored now, so the case reads the
	// same either way -- but joining it onto a bracketed host is what produced the
	// unparseable "[[fd00::1]]:443", so anyone reinstating the header trips here.
	hostile := writeReq(http.MethodPost, "127.0.0.1:9797", map[string]string{
		"Origin": "https://evil.example", "X-Forwarded-Host": "[fd00::1]",
		"X-Forwarded-Proto": "https", "X-Forwarded-Port": "443",
	})
	if !crossOriginWrite(hostile) {
		t.Error("cross-origin write allowed behind an IPv6 X-Forwarded-Host")
	}

	own := writeReq(http.MethodPost, "127.0.0.1:9797", map[string]string{
		"Origin": "https://[fd00::1]", "X-Forwarded-Host": "[fd00::1]",
		"X-Forwarded-Proto": "https",
	})
	if crossOriginWrite(own) {
		t.Error("write from the install's own UI refused behind an IPv6 X-Forwarded-Host")
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
