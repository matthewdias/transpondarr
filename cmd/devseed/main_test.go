package main

import (
	"strings"
	"testing"
)

func TestSeedOnlyCannotWriteTheEnvFile(t *testing.T) {
	if err := validateFlags(true, true); err == nil {
		t.Error("validateFlags(--seed-only, --write-env-local) = nil, want a refusal: there are no endpoints to write")
	}
	for _, c := range []struct{ seedOnly, writeEnv bool }{{true, false}, {false, true}, {false, false}} {
		if err := validateFlags(c.seedOnly, c.writeEnv); err != nil {
			t.Errorf("validateFlags(%v, %v) = %v, want nil", c.seedOnly, c.writeEnv, err)
		}
	}
}

// A seeded queue is only stable while nothing reconciles it against a real
// download client, which is what the blank qBittorrent URL is for.
func TestStubEnvBlanksTheDownloadClient(t *testing.T) {
	env := stubEnv("http://127.0.0.1:1/api", "http://127.0.0.1:2/graphql")
	for _, want := range []string{
		"TRANSPONDARR_TORZNAB_URL=http://127.0.0.1:1/api\n",
		"TRANSPONDARR_ANILIST_ENDPOINT=http://127.0.0.1:2/graphql\n",
		"TRANSPONDARR_QBIT_URL=\n",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("printed env block is missing %q:\n%s", want, env)
		}
	}
}
