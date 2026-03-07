package api

import (
	"fmt"
	"time"
)

var dbTimeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05",
	"2006-01-02 15:04:05Z07:00",
}

// ParseDBTime parses timestamps stored in SQLite/Text fields.
// Supports RFC3339 and SQLite datetime('now') formats.
func ParseDBTime(raw string) (time.Time, error) {
	for _, layout := range dbTimeLayouts {
		if t, err := time.Parse(layout, raw); err == nil {
			// SQLite "YYYY-MM-DD HH:MM:SS" has no timezone; interpret as UTC.
			if layout == "2006-01-02 15:04:05" {
				return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC), nil
			}
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported timestamp format %q", raw)
}
