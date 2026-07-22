package download

import (
	"crypto/sha1"
	"encoding/hex"
	"testing"
)

func TestInfoHashFromMagnet(t *testing.T) {
	const wantHex = "c9e15763f722f23e98a29decdfae341b98d53056"
	cases := map[string]string{
		"hex":               "magnet:?xt=urn:btih:c9e15763f722f23e98a29decdfae341b98d53056&dn=example",
		"hex uppercase":     "magnet:?xt=urn:btih:C9E15763F722F23E98A29DECDFAE341B98D53056",
		"base32":            "magnet:?xt=urn:btih:ZHQVOY7XELZD5GFCTXWN7LRUDOMNKMCW&dn=example",
		"extra xt v2 first": "magnet:?xt=urn:btmh:1220abcd&xt=urn:btih:c9e15763f722f23e98a29decdfae341b98d53056",
	}
	for name, magnet := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := InfoHashFromMagnet(magnet)
			if err != nil {
				t.Fatalf("InfoHashFromMagnet(%q) error: %v", magnet, err)
			}
			if got != wantHex {
				t.Errorf("got %q, want %q", got, wantHex)
			}
		})
	}
}

func TestInfoHashFromMagnetErrors(t *testing.T) {
	for name, magnet := range map[string]string{
		"not magnet": "http://example.com/x.torrent",
		"no btih":    "magnet:?xt=urn:btmh:1220abcd&dn=x",
		"bad length": "magnet:?xt=urn:btih:deadbeef",
		"bad hex":    "magnet:?xt=urn:btih:zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := InfoHashFromMagnet(magnet); err == nil {
				t.Errorf("expected error for %q", magnet)
			}
		})
	}
}

func TestInfoHashFromMeta(t *testing.T) {
	// A minimal but structurally real torrent: a top-level dict with an
	// "announce" string, an "info" dict, and a trailing key to ensure the span
	// scanner stops at the right byte. The info dict itself contains a nested
	// list to exercise recursive scanning.
	info := "d6:lengthi12345e4:name8:test.mkv12:piece lengthi16384e4:tagsl2:hd3:subee"
	meta := "d8:announce20:http://tracker.test/4:info" + info + "13:creation datei1700000000ee"

	want := sha1.Sum([]byte(info))
	wantHex := hex.EncodeToString(want[:])

	got, err := InfoHashFromMeta([]byte(meta))
	if err != nil {
		t.Fatalf("InfoHashFromMeta error: %v", err)
	}
	if got != wantHex {
		t.Errorf("got %q, want %q", got, wantHex)
	}
}

func TestInfoHashFromMetaErrors(t *testing.T) {
	for name, meta := range map[string]string{
		"empty":       "",
		"not a dict":  "i5e",
		"no info key": "d8:announce3:x:ye",
		"truncated":   "d4:infod6:length",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := InfoHashFromMeta([]byte(meta)); err == nil {
				t.Errorf("expected error for %q", meta)
			}
		})
	}
}
