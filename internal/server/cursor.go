package server

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// Keyset paging is opaque to the client: the cursor is base64("sort key|id"),
// the pair every paginated listing here orders by. Activity history sorts on
// created_at and the wanted queue on air date, but the shape is the same, so the
// encoding is shared rather than reinvented per route group.
func keysetCursor(sortKey string, id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(sortKey + "|" + strconv.FormatInt(id, 10)))
}

func decodeKeysetCursor(cursor string) (sortKey string, id int64, err error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", 0, err
	}
	// The LAST separator, because the sort key may itself contain one -- the
	// cutoff listing keys on title names -- while the id never does.
	i := strings.LastIndexByte(string(raw), '|')
	if i < 0 {
		return "", 0, fmt.Errorf("cursor missing separator")
	}
	id, err = strconv.ParseInt(string(raw)[i+1:], 10, 64)
	if err != nil {
		return "", 0, err
	}
	return string(raw)[:i], id, nil
}
