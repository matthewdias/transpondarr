package main

import "testing"

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
