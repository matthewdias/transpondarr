package download

import (
	"crypto/sha1"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/zeebo/bencode"
)

// Info-hash derivation is pure BitTorrent protocol, shared by every client: a
// torrent is keyed by its v1 (BTIH) info hash, and deriving it locally before
// adding gives Add a deterministic ID with no post-add polling or list-diffing.
// A magnet already carries the hash (xt=urn:btih:...); a .torrent file's hash is
// the SHA-1 of the bencoded value of its top-level "info" key.
//
// Magnet parsing (below) is trivial and stays hand-rolled. The .torrent path uses
// zeebo/bencode to capture the raw "info" bytes — the one part that is genuinely
// fiddly to scan by hand — and hashes those exact bytes.

// ErrNoV1InfoHash marks metainfo carrying no v1 info hash: qBittorrent reports
// none for such a torrent, so any hash we derived would match nothing (#165).
var ErrNoV1InfoHash = errors.New("torrent has no v1 info hash")

// InfoHashFromMagnet extracts and normalizes the v1 (BTIH) info hash from a
// magnet URI. Both hex (40 char) and base32 (32 char) encodings are supported;
// the result is lowercase hex.
func InfoHashFromMagnet(magnet string) (string, error) {
	u, err := url.Parse(magnet)
	if err != nil {
		return "", fmt.Errorf("parse magnet: %w", err)
	}
	if u.Scheme != "magnet" {
		return "", fmt.Errorf("not a magnet URI: %q", magnet)
	}
	for _, xt := range u.Query()["xt"] {
		const prefix = "urn:btih:"
		if !strings.HasPrefix(xt, prefix) {
			continue // e.g. urn:btmh: (v2) — unsupported here
		}
		return normalizeBTIH(strings.TrimPrefix(xt, prefix))
	}
	return "", errors.New("magnet has no urn:btih info hash")
}

// normalizeBTIH turns a 40-char hex or 32-char base32 BTIH into lowercase hex.
func normalizeBTIH(s string) (string, error) {
	switch len(s) {
	case 40:
		if _, err := hex.DecodeString(s); err != nil {
			return "", fmt.Errorf("invalid hex info hash %q: %w", s, err)
		}
		return strings.ToLower(s), nil
	case 32:
		raw, err := base32.StdEncoding.DecodeString(strings.ToUpper(s))
		if err != nil {
			return "", fmt.Errorf("invalid base32 info hash %q: %w", s, err)
		}
		return hex.EncodeToString(raw), nil
	default:
		return "", fmt.Errorf("unexpected info hash length %d: %q", len(s), s)
	}
}

// InfoHashFromMeta computes the v1 info hash of a bencoded .torrent file: the
// SHA-1 of the raw bytes of its top-level "info" dictionary value. bencode.RawMessage
// captures those bytes verbatim, so the hash is over the original encoding (a
// re-encode could reorder keys and change the hash). Metainfo carrying no v1
// "pieces" is refused rather than hashed, matching the magnet path's stance on v2.
func InfoHashFromMeta(meta []byte) (string, error) {
	var torrent struct {
		Info bencode.RawMessage `bencode:"info"`
	}
	if err := bencode.DecodeBytes(meta, &torrent); err != nil {
		return "", fmt.Errorf("parse torrent metainfo: %w", err)
	}
	if len(torrent.Info) == 0 {
		return "", errors.New(`torrent metainfo has no "info" key`)
	}
	// Decoding a second time is additive: the hash below still reads the raw bytes.
	var info map[string]bencode.RawMessage
	if err := bencode.DecodeBytes(torrent.Info, &info); err != nil {
		return "", fmt.Errorf(`torrent metainfo has a malformed "info" dictionary: %w`, err)
	}
	// Keyed on "pieces", not on v2's own markers: the question is whether the client
	// will compute a v1 hash over these bytes, which a hybrid torrent does too.
	if _, ok := info["pieces"]; !ok {
		return "", fmt.Errorf(`%w: its "info" dictionary carries no v1 pieces`, ErrNoV1InfoHash)
	}
	sum := sha1.Sum(torrent.Info)
	return hex.EncodeToString(sum[:]), nil
}
