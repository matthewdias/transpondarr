package store

import (
	"encoding/json"
	"testing"
)

// The detail route serializes this straight into a non-optional `aliases: string[]`,
// so every stored value, corrupt ones included, must decode to a list and never null.
func TestTitleAliasesNeverDecodesToNil(t *testing.T) {
	for _, raw := range []string{"[]", "null", "", "not json", `["Hoshi no Kishi 2026"]`} {
		got := TitleAliases(raw)
		b, _ := json.Marshal(got)
		if got == nil || string(b) == "null" {
			t.Errorf("TitleAliases(%q) = %s, want a list", raw, b)
		}
	}
	if got := TitleAliases(EncodeAliases(nil)); len(got) != 0 {
		t.Errorf("round trip of nil = %q, want empty", got)
	}
}
