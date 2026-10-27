package budgets

import (
	"time"

	"beprod/internal/models"
)

type MonthInput struct {
	Month time.Time // civil first-of-month; slice is consecutive months starting at the budget's first month
	Limit int64
	Spent int64
}

type MonthResult struct {
	Month       time.Time
	Limit       int64
	Carryover   int64
	Available   int64
	Spent       int64
	Remaining   int64  // Available - Spent (negative = over)
	PercentUsed int64  // Spent*100/Available, integer floor
	Status      string // "green" | "amber" | "red"
}

// Compute applies rollover (FR-7.3/7.4) and status (FR-7.6) over consecutive months.
func Compute(months []MonthInput, rolloverSince *time.Time) []MonthResult {
	out := make([]MonthResult, 0, len(months))
	for i, in := range months {
		var carry int64
		if i > 0 && rolloverSince != nil && in.Month.After(*rolloverSince) {
			prev := out[i-1]
			if left := prev.Available - prev.Spent; left > 0 {
				carry = left // overspend is never carried
			}
		}
		avail := in.Limit + carry

		var pct int64
		if avail > 0 {
			pct = in.Spent * 100 / avail
		}

		status := "green"
		switch {
		case in.Spent >= avail:
			status = "red"
		case in.Spent*100 >= avail*80:
			status = "amber"
		}

		out = append(out, MonthResult{
			Month: in.Month, Limit: in.Limit, Carryover: carry, Available: avail,
			Spent: in.Spent, Remaining: avail - in.Spent, PercentUsed: pct, Status: status,
		})
	}
	return out
}

// LimitFor returns the limit of the latest entry whose EffectiveMonth <= month.
func LimitFor(limits []models.BudgetLimit, month time.Time) (int64, bool) {
	best := -1
	for i := range limits {
		if limits[i].EffectiveMonth.After(month) {
			continue
		}
		if best < 0 || limits[i].EffectiveMonth.After(limits[best].EffectiveMonth) {
			best = i
		}
	}
	if best < 0 {
		return 0, false
	}
	return limits[best].MonthlyLimit, true
}
