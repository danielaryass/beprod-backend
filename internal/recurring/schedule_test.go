package recurring_test

import (
	"testing"
	"time"

	"beprod/internal/jakarta"
	"beprod/internal/recurring"
)

func civil(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func assertDate(t *testing.T, got, want time.Time) {
	t.Helper()
	if !got.Equal(want) {
		t.Errorf("got %s, want %s", got.Format("2006-01-02"), want.Format("2006-01-02"))
	}
}

// chain asserts successive NextAfter results from start.
func chain(t *testing.T, start time.Time, freq string, want ...time.Time) {
	t.Helper()
	d := start
	for _, w := range want {
		d = recurring.NextAfter(start, freq, d)
		assertDate(t, d, w)
	}
}

func TestAddMonthsClamped(t *testing.T) {
	cases := []struct {
		start time.Time
		n     int
		want  time.Time
	}{
		{civil(2027, 1, 31), 0, civil(2027, 1, 31)},
		{civil(2027, 1, 31), 1, civil(2027, 2, 28)},
		{civil(2027, 1, 31), 2, civil(2027, 3, 31)},
		{civil(2027, 1, 31), 3, civil(2027, 4, 30)},
		{civil(2027, 1, 31), 12, civil(2028, 1, 31)},
		{civil(2027, 1, 31), 13, civil(2028, 2, 29)},
		{civil(2027, 1, 30), 1, civil(2027, 2, 28)},
		{civil(2027, 1, 30), 2, civil(2027, 3, 30)},
		{civil(2026, 12, 15), 1, civil(2027, 1, 15)},
		{civil(2026, 11, 30), 3, civil(2027, 2, 28)},
		{civil(2028, 2, 29), 12, civil(2029, 2, 28)},
		{civil(2028, 2, 29), 48, civil(2032, 2, 29)},
	}
	for _, c := range cases {
		assertDate(t, recurring.AddMonthsClamped(c.start, c.n), c.want)
	}
}

func TestNextAfter_MonthlyFrom31Jan(t *testing.T) {
	chain(t, civil(2027, 1, 31), "monthly",
		civil(2027, 2, 28), civil(2027, 3, 31), civil(2027, 4, 30), civil(2027, 5, 31), civil(2027, 6, 30))
}

func TestNextAfter_MonthlyFrom30Jan(t *testing.T) {
	chain(t, civil(2027, 1, 30), "monthly", civil(2027, 2, 28), civil(2027, 3, 30), civil(2027, 4, 30))
}

func TestNextAfter_MonthlyYearWrap(t *testing.T) {
	chain(t, civil(2026, 11, 15), "monthly", civil(2026, 12, 15), civil(2027, 1, 15), civil(2027, 2, 15))
}

func TestNextAfter_YearlyLeapDay(t *testing.T) {
	chain(t, civil(2028, 2, 29), "yearly",
		civil(2029, 2, 28), civil(2030, 2, 28), civil(2031, 2, 28), civil(2032, 2, 29))
}

func TestNextAfter_YearlyNormal(t *testing.T) {
	chain(t, civil(2026, 10, 20), "yearly", civil(2027, 10, 20), civil(2028, 10, 20))
}

func TestNextAfter_WeeklyKeepsWeekday(t *testing.T) {
	start := civil(2026, 10, 20)
	d := start
	for _, w := range []time.Time{civil(2026, 10, 27), civil(2026, 11, 3), civil(2026, 11, 10)} {
		d = recurring.NextAfter(start, "weekly", d)
		assertDate(t, d, w)
		if d.Weekday() != time.Tuesday {
			t.Errorf("weekday = %s, want Tuesday", d.Weekday())
		}
	}
}

func TestNextAfter_DailyAcrossBoundaries(t *testing.T) {
	cases := []struct{ d, want time.Time }{
		{civil(2026, 10, 31), civil(2026, 11, 1)},
		{civil(2026, 12, 31), civil(2027, 1, 1)},
		{civil(2028, 2, 28), civil(2028, 2, 29)},
		{civil(2029, 2, 28), civil(2029, 3, 1)},
	}
	for _, c := range cases {
		assertDate(t, recurring.NextAfter(c.d, "daily", c.d), c.want)
	}
}

func TestFirstOnOrAfter(t *testing.T) {
	cases := []struct {
		start      time.Time
		freq       string
		from, want time.Time
	}{
		{civil(2026, 11, 1), "monthly", civil(2026, 10, 20), civil(2026, 11, 1)},
		{civil(2026, 11, 1), "monthly", civil(2026, 11, 1), civil(2026, 11, 1)},
		{civil(2026, 1, 31), "monthly", civil(2026, 2, 1), civil(2026, 2, 28)},
		{civil(2026, 1, 31), "monthly", civil(2026, 2, 28), civil(2026, 2, 28)},
		{civil(2026, 1, 31), "monthly", civil(2026, 3, 1), civil(2026, 3, 31)},
		{civil(2026, 10, 20), "weekly", civil(2026, 10, 27), civil(2026, 10, 27)},
		{civil(2026, 10, 20), "weekly", civil(2026, 10, 28), civil(2026, 11, 3)},
		{civil(2026, 10, 1), "daily", civil(2026, 10, 15), civil(2026, 10, 15)},
		{civil(2028, 2, 29), "yearly", civil(2029, 1, 1), civil(2029, 2, 28)},
		{civil(2026, 10, 20), "yearly", civil(2026, 10, 21), civil(2027, 10, 20)},
	}
	for _, c := range cases {
		assertDate(t, recurring.FirstOnOrAfter(c.start, c.freq, c.from), c.want)
	}
}

func TestNextRun(t *testing.T) {
	w := func(y int, m time.Month, d, h, mi, s int) time.Time {
		return time.Date(y, m, d, h, mi, s, 0, jakarta.Loc)
	}
	cases := []struct{ now, want time.Time }{
		{w(2026, 10, 20, 0, 0, 0), w(2026, 10, 20, 0, 5, 0)},
		{w(2026, 10, 20, 0, 4, 0), w(2026, 10, 20, 0, 5, 0)},
		{w(2026, 10, 20, 0, 4, 59), w(2026, 10, 20, 0, 5, 0)},
		{w(2026, 10, 20, 0, 5, 0), w(2026, 10, 21, 0, 5, 0)},
		{w(2026, 10, 20, 12, 0, 0), w(2026, 10, 21, 0, 5, 0)},
		{w(2026, 10, 20, 23, 59, 0), w(2026, 10, 21, 0, 5, 0)},
		{w(2026, 10, 31, 23, 59, 0), w(2026, 11, 1, 0, 5, 0)},
		{w(2026, 12, 31, 23, 59, 0), w(2027, 1, 1, 0, 5, 0)},
		{time.Date(2026, 10, 19, 17, 4, 0, 0, time.UTC), time.Date(2026, 10, 19, 17, 5, 0, 0, time.UTC)},
		{time.Date(2026, 10, 19, 17, 5, 0, 0, time.UTC), time.Date(2026, 10, 20, 17, 5, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		if got := recurring.NextRun(c.now); !got.Equal(c.want) {
			t.Errorf("NextRun(%s) = %s, want %s", c.now, got, c.want)
		}
	}
}
