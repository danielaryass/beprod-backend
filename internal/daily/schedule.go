// Package daily holds the habits + daily notes module.
package daily

import "time"

// Weekdays is a bitmask of ISO weekdays: bit (isoWeekday-1), Monday=1 … Sunday=7.
type Weekdays uint8

const maxStreakDays = 366

// ParseDays builds a Weekdays set; false when empty, out of 1–7, or repeated.
func ParseDays(days []int) (Weekdays, bool) {
	var w Weekdays
	for _, d := range days {
		if d < 1 || d > 7 {
			return 0, false
		}
		bit := Weekdays(1) << (d - 1)
		if w&bit != 0 {
			return 0, false
		}
		w |= bit
	}
	return w, w != 0
}

// List returns the ISO weekdays in ascending order.
func (w Weekdays) List() []int {
	out := []int{}
	for i := 0; i < 7; i++ {
		if w&(Weekdays(1)<<i) != 0 {
			out = append(out, i+1)
		}
	}
	return out
}

// Has reports whether date's weekday is in the set (Sunday counts as 7).
func (w Weekdays) Has(date time.Time) bool {
	iso := int(date.Weekday())
	if iso == 0 {
		iso = 7
	}
	return w&(Weekdays(1)<<(iso-1)) != 0
}

// Streak counts consecutive scheduled days that are done, ending at date.
// A scheduled but pending date does not break the streak. Looks back at most
// 366 days and never before created.
func Streak(days Weekdays, created, date time.Time, done func(time.Time) bool) int {
	cur := date
	if days.Has(cur) && !done(cur) {
		cur = cur.AddDate(0, 0, -1)
	}
	floor := date.AddDate(0, 0, -(maxStreakDays - 1))
	n := 0
	for ; !cur.Before(floor) && !cur.Before(created); cur = cur.AddDate(0, 0, -1) {
		if !days.Has(cur) {
			continue
		}
		if !done(cur) {
			break
		}
		n++
	}
	return n
}
