package store

import (
	"errors"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// IsUniqueViolation reports whether err is SQLite's unique-constraint failure,
// so callers can map it to a conflict without matching on the message text.
func IsUniqueViolation(err error) bool {
	serr, ok := errors.AsType[*sqlite.Error](err)
	return ok && serr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE
}
