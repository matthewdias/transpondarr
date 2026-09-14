package store

import "database/sql"

// MonitorNew is the one read rule for title.monitor_new_from (#188): a newly
// created item is monitored when its number is at or above the title's cut. A
// NULL cut monitors nothing new; a numberless item has no number to compare, so
// it follows whether the title monitors anything new.
func MonitorNew(from, number sql.NullInt64) int64 {
	if !from.Valid {
		return 0
	}
	if !number.Valid {
		return 1
	}
	if number.Int64 >= from.Int64 {
		return 1
	}
	return 0
}
