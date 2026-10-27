package jakarta_test

import (
	"testing"
	"time"

	"beprod/internal/jakarta"
)

// freeze overrides jakarta.Now for the duration of the test.
func freeze(t *testing.T, tm time.Time) {
	t.Helper()
	jakarta.Now = func() time.Time { return tm }
	t.Cleanup(func() { jakarta.Now = time.Now })
}

// utc constructs a time.Time in UTC.
func utc(y int, m time.Month, d, h, mi, s int) time.Time {
	return time.Date(y, m, d, h, mi, s, 0, time.UTC)
}

func TestToday_BeforeMidnightWIB(t *testing.T) {
	freeze(t, utc(2026, 10, 31, 16, 59, 59))
	got := jakarta.Today()
	want := utc(2026, 10, 31, 0, 0, 0)
	if !got.Equal(want) {
		t.Errorf("Today() = %v, want %v", got, want)
	}
}

func TestToday_AtMidnightWIB(t *testing.T) {
	freeze(t, utc(2026, 10, 31, 17, 0, 0))
	got := jakarta.Today()
	want := utc(2026, 11, 1, 0, 0, 0)
	if !got.Equal(want) {
		t.Errorf("Today() = %v, want %v", got, want)
	}
}

func TestToday_YearEnd(t *testing.T) {
	freeze(t, utc(2026, 12, 31, 17, 0, 0))
	got := jakarta.Today()
	want := utc(2027, 1, 1, 0, 0, 0)
	if !got.Equal(want) {
		t.Errorf("Today() = %v, want %v", got, want)
	}
}

func TestDateOf_ConvertsNonUTCInput(t *testing.T) {
	// 2026-10-20 23:30 in zone X (UTC-5) = 2026-10-21 04:30 UTC = 11:30 WIB 21 Oct
	x := time.Date(2026, 10, 20, 23, 30, 0, 0, time.FixedZone("X", -5*3600))
	got := jakarta.DateOf(x)
	want := utc(2026, 10, 21, 0, 0, 0)
	if !got.Equal(want) {
		t.Errorf("DateOf() = %v, want %v", got, want)
	}
}

func TestStartOf(t *testing.T) {
	got := jakarta.StartOf(utc(2026, 10, 20, 0, 0, 0))
	want := utc(2026, 10, 19, 17, 0, 0)
	if !got.Equal(want) {
		t.Errorf("StartOf() = %v, want %v", got, want)
	}
}

func TestMonthStart(t *testing.T) {
	got := jakarta.MonthStart(utc(2026, 10, 31, 0, 0, 0))
	want := utc(2026, 10, 1, 0, 0, 0)
	if !got.Equal(want) {
		t.Errorf("MonthStart() = %v, want %v", got, want)
	}
}

func TestAddMonths(t *testing.T) {
	cases := []struct {
		month time.Time
		n     int
		want  time.Time
	}{
		{utc(2026, 11, 1, 0, 0, 0), 2, utc(2027, 1, 1, 0, 0, 0)},
		{utc(2026, 1, 15, 0, 0, 0), -1, utc(2025, 12, 1, 0, 0, 0)},
		{utc(2026, 10, 20, 0, 0, 0), 0, utc(2026, 10, 1, 0, 0, 0)},
		{utc(2026, 3, 31, 0, 0, 0), 1, utc(2026, 4, 1, 0, 0, 0)},
	}
	for _, c := range cases {
		got := jakarta.AddMonths(c.month, c.n)
		if !got.Equal(c.want) {
			t.Errorf("AddMonths(%v, %d) = %v, want %v", c.month, c.n, got, c.want)
		}
	}
}

func TestParseDate(t *testing.T) {
	got, err := jakarta.ParseDate("2026-10-20")
	if err != nil {
		t.Fatalf("ParseDate('2026-10-20') unexpected error: %v", err)
	}
	want := utc(2026, 10, 20, 0, 0, 0)
	if !got.Equal(want) {
		t.Errorf("ParseDate('2026-10-20') = %v, want %v", got, want)
	}

	for _, s := range []string{"2026-02-30", "20-10-2026", ""} {
		if _, err := jakarta.ParseDate(s); err == nil {
			t.Errorf("ParseDate(%q) expected error, got nil", s)
		}
	}
}

func TestParseMonth(t *testing.T) {
	got, err := jakarta.ParseMonth("2026-10")
	if err != nil {
		t.Fatalf("ParseMonth('2026-10') unexpected error: %v", err)
	}
	want := utc(2026, 10, 1, 0, 0, 0)
	if !got.Equal(want) {
		t.Errorf("ParseMonth('2026-10') = %v, want %v", got, want)
	}

	for _, s := range []string{"2026-13", "2026-1", ""} {
		if _, err := jakarta.ParseMonth(s); err == nil {
			t.Errorf("ParseMonth(%q) expected error, got nil", s)
		}
	}
}

func TestCombine_OK(t *testing.T) {
	cases := []struct {
		hhmm string
		want time.Time
	}{
		{"07:30", utc(2026, 10, 20, 0, 30, 0)},
		{"00:00", utc(2026, 10, 19, 17, 0, 0)},
		{"23:59", utc(2026, 10, 20, 16, 59, 0)},
	}
	for _, c := range cases {
		got, err := jakarta.Combine(utc(2026, 10, 20, 0, 0, 0), c.hhmm)
		if err != nil {
			t.Errorf("Combine(%q) unexpected error: %v", c.hhmm, err)
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("Combine(%q) = %v, want %v", c.hhmm, got, c.want)
		}
	}
}

func TestCombine_Rejects(t *testing.T) {
	for _, s := range []string{"7:30", "24:00", "12:60", "07:30:00", "", "ab:cd"} {
		_, err := jakarta.Combine(utc(2026, 10, 20, 0, 0, 0), s)
		if err == nil {
			t.Errorf("Combine(%q) expected error, got nil", s)
		}
	}
}

func TestFormatDateAndMonth(t *testing.T) {
	d := utc(2026, 10, 5, 0, 0, 0)
	if got := jakarta.FormatDate(d); got != "2026-10-05" {
		t.Errorf("FormatDate() = %q, want %q", got, "2026-10-05")
	}
	if got := jakarta.FormatMonth(d); got != "2026-10" {
		t.Errorf("FormatMonth() = %q, want %q", got, "2026-10")
	}
}

func TestMonthBoundaryWIB(t *testing.T) {
	freeze(t, utc(2026, 9, 30, 17, 0, 0))
	today := jakarta.Today()
	wantToday := utc(2026, 10, 1, 0, 0, 0)
	if !today.Equal(wantToday) {
		t.Errorf("Today() = %v, want %v", today, wantToday)
	}
	ms := jakarta.MonthStart(today)
	wantMS := utc(2026, 10, 1, 0, 0, 0)
	if !ms.Equal(wantMS) {
		t.Errorf("MonthStart(Today()) = %v, want %v", ms, wantMS)
	}
}
