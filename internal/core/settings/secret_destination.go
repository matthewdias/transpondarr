package settings

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ErrSecretRequired reports that a blank secret field could not be filled from
// storage because the request named a different destination.
var ErrSecretRequired = errors.New("secret required")

// inheritSecret fills a blank secret from storage, but only for the destination it
// was saved for: the read path redacts these values, so substituting one into a
// request that names a caller-chosen host sends it straight back out (#259).
func inheritSecret(supplied, stored, toURL, storedURL, what string) (string, error) {
	if supplied != "" || stored == "" {
		return supplied, nil
	}
	// An empty destination is never connected to, so clearing a URL to disable an
	// integration keeps the secret rather than wiping it or being rejected.
	if strings.TrimSpace(toURL) == "" || sameDestination(toURL, storedURL) {
		return stored, nil
	}
	savedFor := storedURL
	if strings.TrimSpace(savedFor) == "" {
		savedFor = "the host it was saved for"
	}
	return "", &SecretDestinationError{What: what, SavedFor: savedFor, To: toURL}
}

// SecretDestinationError is ErrSecretRequired with the destinations it names.
type SecretDestinationError struct {
	What     string // eg. "qBittorrent password"
	SavedFor string
	To       string
}

func (e *SecretDestinationError) Error() string {
	return fmt.Sprintf("%v: the stored %s is only sent to %s; enter the %s for %s",
		ErrSecretRequired, e.What, e.SavedFor, e.What, e.To)
}

func (e *SecretDestinationError) Is(target error) bool { return target == ErrSecretRequired }

// sameDestination reports whether two URLs name the host that receives the secret,
// so a path edit -- switching which Jackett indexer is probed -- is not a new
// destination and costs no retype.
func sameDestination(a, b string) bool {
	da, aok := destination(a)
	db, bok := destination(b)
	if !aok || !bok {
		// url.Parse reads two unrelated bare strings as an identical empty host, so
		// a URL naming no host has to be only itself.
		return strings.TrimSpace(a) == strings.TrimSpace(b)
	}
	return da == db
}

// destination reduces a URL to scheme://host[:port], with a port the scheme implies
// elided so writing it out does not read as a move.
func destination(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	if port := u.Port(); port != "" && !defaultPort(scheme, port) {
		host = net.JoinHostPort(host, port)
	}
	return scheme + "://" + host, true
}

func defaultPort(scheme, port string) bool {
	return (scheme == "http" && port == "80") || (scheme == "https" && port == "443")
}
