package goals

import (
	"time"

	"beprod/internal/jakarta"
)

// Progress is a goal's progress derived from its account balance. Formulas: PLAN-DESIGN.md 5.5.
type Progress struct {
	Saved   int64
	Percent int64
	Monthly *int64
	Unsaved int64
	Reached bool
}

// Calc computes progress. balanceAtMonthStart is the account balance at the end of last month.
func Calc(target, balance, balanceAtMonthStart int64, today time.Time, targetDate *time.Time) Progress {
	p := Progress{Saved: max(0, balance)}
	p.Percent = min(100, p.Saved*100/target)
	p.Reached = p.Saved >= target
	if targetDate == nil {
		return p
	}
	start := max(0, balanceAtMonthStart)
	t, m := jakarta.MonthStart(*targetDate), jakarta.MonthStart(today)
	left := int64((t.Year()-m.Year())*12+int(t.Month()-m.Month())) + 1
	left = max(1, left)
	monthly := max(0, (target-start+left-1)/left)
	p.Monthly = &monthly
	if !p.Reached {
		p.Unsaved = max(0, monthly-(p.Saved-start))
	}
	return p
}
