package domain

import "time"

// UTCTime returns the time normalized to UTC.
func UTCTime(t time.Time) time.Time {
	return t.UTC()
}

// UTCTimePtr returns a pointer to the time normalized to UTC, or nil if the input is nil.
func UTCTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	utc := t.UTC()
	return &utc
}
