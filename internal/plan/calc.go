package plan

import (
	"time"

	"beprod/internal/accounts"
)

// NextPayday is the first payday (day clamped to the month's end) strictly after today.
func NextPayday(today time.Time, day int) time.Time {
	if p := accounts.ClampDay(today.Year(), today.Month(), day); p.After(today) {
		return p
	}
	return accounts.ClampDay(today.Year(), today.Month()+1, day)
}

// DaysUntil counts days from today to payday, today included; payday is after today so it is at least 1.
func DaysUntil(today, payday time.Time) int {
	return int(payday.Sub(today).Hours() / 24)
}

// EmergencyLevel maps months of cover, in tenths, to low (<3), mid (<6) or high.
func EmergencyLevel(tenths int64) string {
	switch {
	case tenths < 30:
		return "low"
	case tenths < 60:
		return "mid"
	}
	return "high"
}
