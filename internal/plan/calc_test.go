package plan

import (
	"testing"
	"time"
)

func d(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) }

func TestNextPayday(t *testing.T) {
	cases := []struct {
		today time.Time
		day   int
		want  time.Time
	}{
		{d(2026, 10, 20), 25, d(2026, 10, 25)},
		{d(2026, 10, 25), 25, d(2026, 11, 25)},
		{d(2026, 10, 26), 25, d(2026, 11, 25)},
		{d(2026, 11, 10), 31, d(2026, 11, 30)},
		{d(2026, 11, 30), 31, d(2026, 12, 31)},
	}
	for _, c := range cases {
		if got := NextPayday(c.today, c.day); !got.Equal(c.want) {
			t.Errorf("NextPayday(%s, %d) = %s, want %s", c.today.Format("2006-01-02"), c.day, got.Format("2006-01-02"), c.want.Format("2006-01-02"))
		}
	}
}

func TestDaysUntil(t *testing.T) {
	if got := DaysUntil(d(2026, 10, 20), d(2026, 10, 25)); got != 5 {
		t.Errorf("got %d, want 5", got)
	}
	if got := DaysUntil(d(2026, 10, 24), d(2026, 10, 25)); got != 1 {
		t.Errorf("got %d, want 1", got)
	}
}

func TestEmergencyLevel(t *testing.T) {
	for tenths, want := range map[int64]string{29: "low", 30: "mid", 59: "mid", 60: "high"} {
		if got := EmergencyLevel(tenths); got != want {
			t.Errorf("EmergencyLevel(%d) = %s, want %s", tenths, got, want)
		}
	}
}
