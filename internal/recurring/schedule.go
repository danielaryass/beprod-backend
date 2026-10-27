package recurring

import "time"

// AddMonthsClamped returns start's day-of-month n months later, clamped to the last day of that month.
func AddMonthsClamped(start time.Time, n int) time.Time {
	first := time.Date(start.Year(), start.Month()+time.Month(n), 1, 0, 0, 0, 0, time.UTC) // Go normalizes months > 12
	last := time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()        // last day of target month
	day := start.Day()
	if day > last {
		day = last
	}
	return time.Date(first.Year(), first.Month(), day, 0, 0, 0, 0, time.UTC)
}

func monthsBetween(start, d time.Time) int {
	return (d.Year()-start.Year())*12 + int(d.Month()-start.Month())
}

// NextAfter returns the schedule date after d (d is start or a previous NextAfter result).
func NextAfter(start time.Time, freq string, d time.Time) time.Time {
	switch freq {
	case "weekly":
		return d.AddDate(0, 0, 7)
	case "monthly":
		return AddMonthsClamped(start, monthsBetween(start, d)+1)
	case "yearly":
		return AddMonthsClamped(start, 12*((d.Year()-start.Year())+1))
	default:
		// unknown frequency: validated upstream; fall back to daily so callers always advance
		return d.AddDate(0, 0, 1)
	}
}

// FirstOnOrAfter returns the first schedule date >= from (start if from <= start).
func FirstOnOrAfter(start time.Time, freq string, from time.Time) time.Time {
	d := start
	// ponytail: loops from start; fine for decades of daily rules
	for d.Before(from) {
		d = NextAfter(start, freq, d)
	}
	return d
}
