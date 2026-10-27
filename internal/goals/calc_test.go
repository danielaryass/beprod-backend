package goals_test

import (
	"testing"
	"time"

	"beprod/internal/goals"
)

func TestCalc(t *testing.T) {
	today := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	date := func(y int, m time.Month, d int) *time.Time {
		v := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
		return &v
	}
	i64 := func(v int64) *int64 { return &v }
	cases := []struct {
		name                    string
		target, balance, start  int64
		date                    *time.Time
		saved, percent, unsaved int64
		monthly                 *int64
		reached                 bool
	}{
		{"with date", 12_000_000, 3_000_000, 2_000_000, date(2027, 3, 15), 3_000_000, 25, 666_667, i64(1_666_667), false},
		{"no date", 12_000_000, 3_000_000, 2_000_000, nil, 3_000_000, 25, 0, nil, false},
		{"negative balance", 12_000_000, -50_000, 0, nil, 0, 0, 0, nil, false},
		{"reached", 12_000_000, 13_000_000, 2_000_000, date(2027, 3, 15), 13_000_000, 100, 0, i64(1_666_667), true},
		{"this month", 12_000_000, 3_000_000, 2_000_000, date(2026, 10, 30), 3_000_000, 25, 9_000_000, i64(10_000_000), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := goals.Calc(c.target, c.balance, c.start, today, c.date)
			if p.Saved != c.saved || p.Percent != c.percent || p.Unsaved != c.unsaved || p.Reached != c.reached {
				t.Errorf("got %+v", p)
			}
			if (p.Monthly == nil) != (c.monthly == nil) || (p.Monthly != nil && *p.Monthly != *c.monthly) {
				t.Errorf("monthly = %v, want %v", p.Monthly, c.monthly)
			}
		})
	}
}
