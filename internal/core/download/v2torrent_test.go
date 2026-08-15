package download

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeebo/bencode"
)

// Fixtures for the #165 spike: a BEP52 v2-only torrent has an "info" dict like
// any other, so InfoHashFromMeta hashes it with SHA-1 and returns a well-formed
// hash no client will ever report. The v1 fixture is the control — simple enough
// that its failure is unambiguous — so a rejected v2 file can be attributed.

const (
	// BEP52 fixes the merkle leaf at 16 KiB; one leaf per piece, and exactly two
	// leaves, so the tree is already a power of two and needs no zero padding.
	spikeBlockSize  = 16384
	spikePieceLen   = 16384
	spikePayloadLen = 2 * spikeBlockSize

	// RFC 2606 reserves .invalid, so the artifact cannot announce to a real tracker.
	spikeAnnounce = "http://tracker.invalid/announce"

	// Synthetic names, structurally release-shaped; distinct so the two artifacts
	// cannot collide on save path in a live client.
	spikeV1Name = "[SynthGrp] Placeholder Spike - 01 [1080p][A1B2C3D4].mkv"
	spikeV2Name = "[SynthGrp] Placeholder Spike - 02 [1080p][E5F6A7B8].mkv"
)

// synthPayload is deterministic filler, so every rebuild yields the same hashes.
func synthPayload() []byte {
	b := make([]byte, spikePayloadLen)
	for i := range b {
		b[i] = byte(i % 251)
	}
	return b
}

func bencodeValue(t *testing.T, v any) []byte {
	t.Helper()
	b, err := bencode.EncodeBytes(v)
	if err != nil {
		t.Fatalf("bencode encode: %v", err)
	}
	return b
}

// bstr bencodes a string, so the hand-spliced top-level dict below needs no
// counted byte lengths.
func bstr(s string) string { return fmt.Sprintf("%d:%s", len(s), s) }

// buildV2OnlyMeta returns BEP52 v2-only metainfo and the exact info-dict bytes it
// embeds: no "pieces", "length" or "files", which is precisely the silent path.
func buildV2OnlyMeta(t *testing.T, name string) (meta, rawInfo []byte) {
	t.Helper()
	payload := synthPayload()
	l0 := sha256.Sum256(payload[:spikeBlockSize])
	l1 := sha256.Sum256(payload[spikeBlockSize:])
	layer := append(append([]byte{}, l0[:]...), l1[:]...)
	root := sha256.Sum256(layer)

	rawInfo = bencodeValue(t, map[string]any{
		"file tree": map[string]any{
			name: map[string]any{
				"": map[string]any{
					"length":      spikePayloadLen,
					"pieces root": root[:],
				},
			},
		},
		"meta version": 2,
		"name":         name,
		"piece length": spikePieceLen,
	})
	// The payload exceeds one piece, so BEP52 requires the piece layer; at one
	// block per piece that layer is the leaf layer itself.
	pieceLayers := bencodeValue(t, map[string]any{string(root[:]): layer})

	meta = []byte("d" + bstr("announce") + bstr(spikeAnnounce) +
		bstr("info") + string(rawInfo) +
		bstr("piece layers") + string(pieceLayers) + "e")
	return meta, rawInfo
}

// buildV1OnlyMeta returns classic v1 metainfo over the same payload.
func buildV1OnlyMeta(t *testing.T, name string) (meta, rawInfo []byte) {
	t.Helper()
	payload := synthPayload()
	p0 := sha1.Sum(payload[:spikePieceLen])
	p1 := sha1.Sum(payload[spikePieceLen:])

	rawInfo = bencodeValue(t, map[string]any{
		"length":       spikePayloadLen,
		"name":         name,
		"piece length": spikePieceLen,
		"pieces":       append(append([]byte{}, p0[:]...), p1[:]...),
	})
	meta = []byte("d" + bstr("announce") + bstr(spikeAnnounce) +
		bstr("info") + string(rawInfo) + "e")
	return meta, rawInfo
}

// buildHybridMeta returns metainfo carrying both formats. The payload is exactly
// two 16 KiB pieces, so v1 pieces and v2 blocks already align and BEP47 padding
// files — the fiddly part of a hybrid — are not needed.
func buildHybridMeta(t *testing.T, name string) (meta, rawInfo []byte) {
	t.Helper()
	payload := synthPayload()
	p0 := sha1.Sum(payload[:spikePieceLen])
	p1 := sha1.Sum(payload[spikePieceLen:])
	l0 := sha256.Sum256(payload[:spikeBlockSize])
	l1 := sha256.Sum256(payload[spikeBlockSize:])
	layer := append(append([]byte{}, l0[:]...), l1[:]...)
	root := sha256.Sum256(layer)

	rawInfo = bencodeValue(t, map[string]any{
		"file tree": map[string]any{
			name: map[string]any{
				"": map[string]any{
					"length":      spikePayloadLen,
					"pieces root": root[:],
				},
			},
		},
		"length":       spikePayloadLen,
		"meta version": 2,
		"name":         name,
		"piece length": spikePieceLen,
		"pieces":       append(append([]byte{}, p0[:]...), p1[:]...),
	})
	pieceLayers := bencodeValue(t, map[string]any{string(root[:]): layer})

	meta = []byte("d" + bstr("announce") + bstr(spikeAnnounce) +
		bstr("info") + string(rawInfo) +
		bstr("piece layers") + string(pieceLayers) + "e")
	return meta, rawInfo
}

// TestInfoHashFromMetaHybrid is the guard against over-rejecting: anime trackers
// publish v1 and hybrid essentially universally, so refusing a hybrid would break
// the common case in the name of one nobody has yet observed.
func TestInfoHashFromMetaHybrid(t *testing.T) {
	meta, rawInfo := buildHybridMeta(t, spikeV2Name)

	// The fixture is only a guard if it really carries both shapes.
	var info map[string]bencode.RawMessage
	if err := bencode.DecodeBytes(rawInfo, &info); err != nil {
		t.Fatalf("decode fixture info dict: %v", err)
	}
	for _, k := range []string{"pieces", "file tree", "meta version"} {
		if _, ok := info[k]; !ok {
			t.Fatalf("fixture is not hybrid: info dict lacks %q", k)
		}
	}

	sum := sha1.Sum(rawInfo)
	want := hex.EncodeToString(sum[:])

	got, err := InfoHashFromMeta(meta)
	if err != nil {
		t.Fatalf("InfoHashFromMeta rejected hybrid metainfo: %v", err)
	}
	// Not the control's hash — a different torrent — but the same kind of value,
	// derived the same way: what a client reports as infohash_v1.
	if got != want {
		t.Errorf("got %q, want the SHA-1 of the info dict %q", got, want)
	}
}

// TestInfoHashFromMetaV2Only pins #165's fix: the SHA-1 we would derive for a
// v2-only torrent is neither form BEP52 lets a client key it by, and qBittorrent
// reports no v1 hash at all for one, so the release is refused instead.
func TestInfoHashFromMetaV2Only(t *testing.T) {
	meta, rawInfo := buildV2OnlyMeta(t, spikeV2Name)

	// The fixture is only evidence if it really carries no v1 shape.
	var info map[string]bencode.RawMessage
	if err := bencode.DecodeBytes(rawInfo, &info); err != nil {
		t.Fatalf("decode fixture info dict: %v", err)
	}
	for _, k := range []string{"pieces", "length", "files"} {
		if _, ok := info[k]; ok {
			t.Fatalf("fixture is not v2-only: info dict carries %q", k)
		}
	}
	var metaVersion int
	if err := bencode.DecodeBytes(info["meta version"], &metaVersion); err != nil {
		t.Fatalf(`fixture has no readable "meta version": %v`, err)
	}
	if metaVersion != 2 {
		t.Fatalf("fixture meta version = %d, want 2", metaVersion)
	}

	got, err := InfoHashFromMeta(meta)
	if err == nil {
		v2 := sha256.Sum256(rawInfo)
		t.Fatalf("InfoHashFromMeta accepted v2-only metainfo, returning %q\n"+
			"  BEP52 v2 (SHA-256)        %s\n  BEP52 v2 truncated (20B)  %s\n"+
			"neither of which it matches, so no client will ever report it",
			got, hex.EncodeToString(v2[:]), hex.EncodeToString(v2[:20]))
	}
	// Pinned to the cause: malformed metainfo errors too, and would keep this green
	// over a fixture that had stopped being a v2 torrent at all.
	if !errors.Is(err, ErrNoV1InfoHash) {
		t.Errorf("error = %v, want ErrNoV1InfoHash", err)
	}
}

// TestInfoHashFromMagnetV2Only pins the contrast #165 draws: the same torrent
// named by a v2 magnet is refused loudly, where the .torrent path is silent.
func TestInfoHashFromMagnetV2Only(t *testing.T) {
	_, rawInfo := buildV2OnlyMeta(t, spikeV2Name)
	v2 := sha256.Sum256(rawInfo)
	// 0x12 0x20 is the multihash prefix for a 32-byte sha2-256 digest.
	magnet := "magnet:?xt=urn:btmh:1220" + hex.EncodeToString(v2[:]) + "&dn=spike"

	got, err := InfoHashFromMagnet(magnet)
	if err == nil {
		t.Fatalf("InfoHashFromMagnet accepted a v2-only magnet, returning %q", got)
	}
	// Pinned to the reason: a merely malformed magnet errors too, and would keep
	// this green over a fixture that had stopped being a v2 magnet at all.
	if !strings.Contains(err.Error(), "no urn:btih info hash") {
		t.Errorf("error = %v, want the missing-btih refusal", err)
	}
}

// TestInfoHashFromMetaV1Control checks the builder itself, so a v2 artifact a
// client rejects can be attributed to its v2-ness rather than to this code.
func TestInfoHashFromMetaV1Control(t *testing.T) {
	meta, rawInfo := buildV1OnlyMeta(t, spikeV1Name)

	sum := sha1.Sum(rawInfo)
	want := hex.EncodeToString(sum[:])

	got, err := InfoHashFromMeta(meta)
	if err != nil {
		t.Fatalf("InfoHashFromMeta on the v1 control: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	t.Logf("v1 control %s\n  InfoHashFromMeta (SHA-1)  %s", spikeV1Name, got)
}

// TestWriteSpikeArtifacts writes the two .torrent files for the live half of the
// #165 spike. Skipped by default: the artifacts are reproducible from this test,
// so nothing binary is committed.
func TestWriteSpikeArtifacts(t *testing.T) {
	dir := os.Getenv("TRANSPONDARR_SPIKE_OUT")
	if dir == "" {
		// -count=1 is load-bearing: a cached pass writes no files, so a rebuild
		// into an existing directory would report ok and leave stale ones.
		t.Skip("to write the #165 spike torrents:\n" +
			`  TRANSPONDARR_SPIKE_OUT=<dir> go test ./internal/core/download/ \` + "\n" +
			"    -run TestWriteSpikeArtifacts -v -count=1")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create %s: %v", dir, err)
	}

	for _, tc := range []struct {
		file, name string
		build      func(*testing.T, string) ([]byte, []byte)
	}{
		{"spike165-v1-control.torrent", spikeV1Name, buildV1OnlyMeta},
		{"spike165-v2-only.torrent", spikeV2Name, buildV2OnlyMeta},
	} {
		meta, rawInfo := tc.build(t, tc.name)
		path := filepath.Join(dir, tc.file)
		if err := os.WriteFile(path, meta, 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		s1 := sha1.Sum(rawInfo)
		s2 := sha256.Sum256(rawInfo)
		t.Logf("wrote %s (%d bytes)\n  name                      %s\n"+
			"  InfoHashFromMeta (SHA-1)  %s\n  BEP52 v2 (SHA-256)        %s\n  BEP52 v2 truncated (20B)  %s",
			path, len(meta), tc.name,
			hex.EncodeToString(s1[:]), hex.EncodeToString(s2[:]), hex.EncodeToString(s2[:20]))
	}
}
