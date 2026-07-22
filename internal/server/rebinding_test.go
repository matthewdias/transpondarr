package server

import (
	"net/http"
	"testing"
)

func TestHostIsLocalLiteral(t *testing.T) {
	cases := map[string]bool{
		"localhost":         true,
		"localhost:9797":    true,
		"127.0.0.1:9797":    true,
		"192.168.1.10:9797": true,
		"10.0.0.5":          true,
		"[::1]:9797":        true,
		"evil.com":          false,
		"evil.com:9797":     false,
		"transpondarr.lan":  false,
	}
	for host, want := range cases {
		if got := hostIsLocalLiteral(host); got != want {
			t.Errorf("hostIsLocalLiteral(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestIsLocalRequestRejectsRebinding(t *testing.T) {
	// Private-range peer (would pass the IP check) but addressed by a domain name:
	// the DNS-rebinding shape. The bypass must not apply.
	req := &http.Request{
		RemoteAddr: "192.168.1.50:54321",
		Host:       "evil.com",
		Header:     http.Header{},
	}
	if isLocalRequest(req) {
		t.Fatal("rebinding request (private peer, domain Host) treated as local")
	}

	// Same peer, addressed by IP literal: the legitimate LAN case.
	req.Host = "192.168.1.10:9797"
	if !isLocalRequest(req) {
		t.Fatal("legitimate private-IP request not treated as local")
	}

	// Any proxy header disables the bypass regardless of Host.
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	if isLocalRequest(req) {
		t.Fatal("proxied request treated as local")
	}
}
