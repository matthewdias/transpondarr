package store

import "time"

// TimestampLayout is SQLite's datetime('now') output (UTC, no zone) — the form
// every timestamp column in this database holds.
const TimestampLayout = "2006-01-02 15:04:05"

// FormatTimestamp renders t in the stored form.
func FormatTimestamp(t time.Time) string {
	return t.UTC().Format(TimestampLayout)
}

// ParseTimestamp reads a stored timestamp back as the UTC instant it names.
func ParseTimestamp(s string) (time.Time, error) {
	return time.Parse(TimestampLayout, s)
}
