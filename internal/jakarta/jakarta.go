package jakarta

import (
	"fmt"
	"regexp"
	"time"
)

// Loc is the WIB timezone (UTC+7). Jakarta has no DST.
var Loc = time.FixedZone("WIB", 7*60*60)

// Now returns the current time. Tests can override this via testutil.SetNow.
var Now = time.Now

// reHHMM validates "HH:MM" format where HH is 00-23 and MM is 00-59.
var reHHMM = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// Today returns the civil date of the current instant in WIB, as 00:00 UTC.
func Today() time.Time {
	return DateOf(Now())
}

// DateOf returns the civil date of instant t in WIB, as 00:00 UTC.
func DateOf(t time.Time) time.Time {
	y, m, d := t.In(Loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// StartOf returns the instant of 00:00 WIB on the civil date.
func StartOf(date time.Time) time.Time {
	y, m, d := date.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, Loc)
}

// MonthStart returns the civil first day of date's month (00:00 UTC).
func MonthStart(date time.Time) time.Time {
	y, m, _ := date.Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
}

// AddMonths returns MonthStart(month) + n months (n may be negative).
func AddMonths(month time.Time, n int) time.Time {
	ms := MonthStart(month)
	return time.Date(ms.Year(), ms.Month()+time.Month(n), 1, 0, 0, 0, 0, time.UTC)
}

// ParseDate parses "2006-01-02" into a civil date (00:00 UTC).
// Returns error if input is invalid.
func ParseDate(s string) (time.Time, error) {
	return time.Parse("2006-01-02", s)
}

// ParseMonth parses "2006-01" into a civil first-of-month (00:00 UTC).
// Returns error if input is invalid.
func ParseMonth(s string) (time.Time, error) {
	return time.Parse("2006-01", s)
}

// Combine combines a civil date with an "HH:MM" string to produce an instant in WIB.
// Returns an error if hhmm does not match the expected format.
func Combine(date time.Time, hhmm string) (time.Time, error) {
	if !reHHMM.MatchString(hhmm) {
		return time.Time{}, fmt.Errorf("invalid time %q, want HH:MM", hhmm)
	}
	var h, mi int
	fmt.Sscanf(hhmm, "%d:%d", &h, &mi)
	y, m, d := date.Date()
	return time.Date(y, m, d, h, mi, 0, 0, Loc), nil
}

// FormatDate formats a civil date as "2006-01-02".
func FormatDate(date time.Time) string {
	return date.Format("2006-01-02")
}

// FormatMonth formats a month as "2006-01".
func FormatMonth(month time.Time) string {
	return month.Format("2006-01")
}
