package store

import "encoding/json"

// TitleAliases decodes series.aliases (#345), never to nil. Only EncodeAliases
// writes the column, so a value it cannot decode degrades to no aliases.
func TitleAliases(raw string) []string {
	var out []string
	if json.Unmarshal([]byte(raw), &out) != nil || out == nil {
		return []string{}
	}
	return out
}

// EncodeAliases is series.aliases' one writer format; nil encodes as [] so the
// column never holds JSON null.
func EncodeAliases(aliases []string) string {
	if aliases == nil {
		aliases = []string{}
	}
	b, _ := json.Marshal(aliases) //nolint:errchkjson // a []string always marshals
	return string(b)
}
