package daily

import (
	"reflect"
	"testing"
	"time"

	"beprod/internal/jakarta"
)

func d(s string) time.Time { t, _ := jakarta.ParseDate(s); return t }

func set(dates ...string) func(time.Time) bool {
	m := map[string]bool{}
	for _, s := range dates {
		m[s] = true
	}
	return func(t time.Time) bool { return m[jakarta.FormatDate(t)] }
}

func TestParseDays(t *testing.T) {
	w, ok := ParseDays([]int{5, 1, 3})
	if !ok || !reflect.DeepEqual(w.List(), []int{1, 3, 5}) {
		t.Fatalf("got %v %v", w.List(), ok)
	}
	for _, bad := range [][]int{nil, {}, {0}, {8}, {1, 1}} {
		if _, ok := ParseDays(bad); ok {
			t.Errorf("ParseDays(%v) should fail", bad)
		}
	}
	sun, _ := ParseDays([]int{7})
	if !sun.Has(d("2026-10-25")) {
		t.Error("Sunday should count as 7")
	}
	if sun.Has(d("2026-10-26")) {
		t.Error("Monday is not 7")
	}
}

func TestStreak(t *testing.T) {
	every, _ := ParseDays([]int{1, 2, 3, 4, 5, 6, 7})
	mwf, _ := ParseDays([]int{1, 3, 5})
	cases := []struct {
		name    string
		days    Weekdays
		created string
		done    []string
		want    int
	}{
		{"done through today", every, "2026-01-01", []string{"2026-10-25", "2026-10-26", "2026-10-27"}, 3},
		{"today pending doesn't break", every, "2026-01-01", []string{"2026-10-25", "2026-10-26"}, 2},
		{"gap breaks", every, "2026-01-01", []string{"2026-10-24", "2026-10-26"}, 1},
		{"unscheduled days skipped", mwf, "2026-01-01", []string{"2026-10-21", "2026-10-23", "2026-10-26"}, 3},
		{"missed scheduled day stops", mwf, "2026-01-01", []string{"2026-10-19", "2026-10-23", "2026-10-26"}, 2},
		{"stops at creation", every, "2026-10-26", []string{"2026-10-25", "2026-10-26", "2026-10-27"}, 2},
		{"nothing done", every, "2026-01-01", nil, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Streak(c.days, d(c.created), d("2026-10-27"), set(c.done...)); got != c.want {
				t.Fatalf("got %d want %d", got, c.want)
			}
		})
	}
	if got := Streak(every, d("2024-01-01"), d("2026-10-27"), func(time.Time) bool { return true }); got != 366 {
		t.Fatalf("cap: got %d want 366", got)
	}
}
