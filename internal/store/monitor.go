package store

import "database/sql"

// MonitorNew is the one read rule for series.monitor_new_from (#188): a newly
// created item is monitored when its number reaches the series' cut. A NULL cut
// monitors nothing new; a numberless item has no number to compare, so it
// follows whether the series wants anything new at all.
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
