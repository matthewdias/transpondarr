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
	key, idStr, ok := strings.Cut(string(raw), "|")
	if !ok {
		return "", 0, fmt.Errorf("cursor missing separator")
	}
	id, err = strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return "", 0, err
	}
	return key, id, nil
}
