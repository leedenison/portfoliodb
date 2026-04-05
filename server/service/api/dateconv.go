package api

import (
	"time"

	"google.golang.org/genproto/googleapis/type/date"
)

// dateToTime converts a google.type.Date to time.Time (UTC, midnight).
// Returns zero time if d is nil or has a zero year.
func dateToTime(d *date.Date) time.Time {
	if d == nil || d.Year == 0 {
		return time.Time{}
	}
	return time.Date(int(d.Year), time.Month(d.Month), int(d.Day), 0, 0, 0, 0, time.UTC)
}

// dateToTimePtr converts a google.type.Date to *time.Time. Returns nil if d is nil or zero.
func dateToTimePtr(d *date.Date) *time.Time {
	if d == nil || d.Year == 0 {
		return nil
	}
	t := time.Date(int(d.Year), time.Month(d.Month), int(d.Day), 0, 0, 0, 0, time.UTC)
	return &t
}

// timeToDate converts a time.Time to a google.type.Date.
func timeToDate(t time.Time) *date.Date {
	return &date.Date{
		Year:  int32(t.Year()),
		Month: int32(t.Month()),
		Day:   int32(t.Day()),
	}
}
