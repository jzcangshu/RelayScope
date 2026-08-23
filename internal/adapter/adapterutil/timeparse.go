package adapterutil

import "time"

const milliBoundary = 100_000_000_000 // 1e11; values above this are milliseconds

// ParseFlexibleTime interprets a numeric timestamp as either Unix seconds
// or Unix milliseconds based on magnitude. Values <= 0 produce the zero time.
func ParseFlexibleTime(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	if value > milliBoundary {
		return time.UnixMilli(value).UTC()
	}
	return time.Unix(value, 0).UTC()
}
