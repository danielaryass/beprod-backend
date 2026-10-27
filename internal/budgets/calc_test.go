package budgets_test

import (
	"testing"
	"time"

	"beprod/internal/budgets"
	"beprod/internal/models"
)

func m(year int, month time.Month) time.Time {
	return time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
}

func ptr(t time.Time) *time.Time { return &t }

func check(t *testing.T, got budgets.MonthResult, carry, avail, spent, remaining, pct int64, status string) {
	t.Helper()
	mo := got.Month.Format("2006-01")
	if got.Carryover != carry {
		t.Errorf("%s carryover = %d, want %d", mo, got.Carryover, carry)
	}
	if got.Available != avail {
		t.Errorf("%s available = %d, want %d", mo, got.Available, avail)
	}
	if got.Spent != spent {
		t.Errorf("%s spent = %d, want %d", mo, got.Spent, spent)
	}
	if got.Remaining != remaining {
		t.Errorf("%s remaining = %d, want %d", mo, got.Remaining, remaining)
	}
	if got.PercentUsed != pct {
		t.Errorf("%s percent = %d, want %d", mo, got.PercentUsed, pct)
	}
	if got.Status != status {
		t.Errorf("%s status = %q, want %q", mo, got.Status, status)
	}
}

const lim = 1000000

func in(mo time.Time, limit, spent int64) budgets.MonthInput {
	return budgets.MonthInput{Month: mo, Limit: limit, Spent: spent}
}

func TestCompute_US72_AC1_RolloverCarriesUnused(t *testing.T) {
	r := budgets.Compute([]budgets.MonthInput{in(m(2026, 9), lim, 800000), in(m(2026, 10), lim, 0)}, ptr(m(2026, 9)))
	check(t, r[0], 0, 1000000, 800000, 200000, 80, "amber")
	check(t, r[1], 200000, 1200000, 0, 1200000, 0, "green")
}

func TestCompute_US72_AC2_OverspendNotCarried(t *testing.T) {
	r := budgets.Compute([]budgets.MonthInput{in(m(2026, 9), lim, 1200000), in(m(2026, 10), lim, 0)}, ptr(m(2026, 9)))
	check(t, r[0], 0, 1000000, 1200000, -200000, 120, "red")
	check(t, r[1], 0, 1000000, 0, 1000000, 0, "green")
}

func TestCompute_US72_AC3_RolloverOff(t *testing.T) {
	r := budgets.Compute([]budgets.MonthInput{in(m(2026, 9), lim, 800000), in(m(2026, 10), lim, 0)}, nil)
	check(t, r[0], 0, 1000000, 800000, 200000, 80, "amber")
	check(t, r[1], 0, 1000000, 0, 1000000, 0, "green")
}

func TestCompute_Compounding3Months(t *testing.T) {
	r := budgets.Compute([]budgets.MonthInput{in(m(2026, 7), lim, 800000), in(m(2026, 8), lim, 500000), in(m(2026, 9), lim, 0)}, ptr(m(2026, 7)))
	check(t, r[0], 0, 1000000, 800000, 200000, 80, "amber")
	check(t, r[1], 200000, 1200000, 500000, 700000, 41, "green")
	check(t, r[2], 700000, 1700000, 0, 1700000, 0, "green")
}

func TestCompute_NoCarryInRolloverSinceMonth(t *testing.T) {
	r := budgets.Compute([]budgets.MonthInput{in(m(2026, 9), lim, 100000), in(m(2026, 10), lim, 400000), in(m(2026, 11), lim, 0)}, ptr(m(2026, 10)))
	check(t, r[0], 0, 1000000, 100000, 900000, 10, "green")
	check(t, r[1], 0, 1000000, 400000, 600000, 40, "green")
	check(t, r[2], 600000, 1600000, 0, 1600000, 0, "green")
}

func TestCompute_RolloverSinceInMiddleOfHistory(t *testing.T) {
	r := budgets.Compute([]budgets.MonthInput{in(m(2026, 7), lim, 0), in(m(2026, 8), lim, 0), in(m(2026, 9), lim, 400000), in(m(2026, 10), lim, 0)}, ptr(m(2026, 9)))
	check(t, r[0], 0, 1000000, 0, 1000000, 0, "green")
	check(t, r[1], 0, 1000000, 0, 1000000, 0, "green")
	check(t, r[2], 0, 1000000, 400000, 600000, 40, "green")
	check(t, r[3], 600000, 1600000, 0, 1600000, 0, "green")
}

func TestCompute_OverspendFloorsCarryAtZero(t *testing.T) {
	r := budgets.Compute([]budgets.MonthInput{in(m(2026, 9), lim, 1500000), in(m(2026, 10), lim, 300000), in(m(2026, 11), lim, 0)}, ptr(m(2026, 9)))
	check(t, r[0], 0, 1000000, 1500000, -500000, 150, "red")
	check(t, r[1], 0, 1000000, 300000, 700000, 30, "green")
	check(t, r[2], 700000, 1700000, 0, 1700000, 0, "green")
}

func TestCompute_SingleMonth(t *testing.T) {
	r := budgets.Compute([]budgets.MonthInput{in(m(2026, 10), 500000, 100000)}, ptr(m(2026, 10)))
	if len(r) != 1 {
		t.Fatalf("len = %d, want 1", len(r))
	}
	check(t, r[0], 0, 500000, 100000, 400000, 20, "green")
}

func TestCompute_SingleMonthRolloverSinceEarlier(t *testing.T) {
	r := budgets.Compute([]budgets.MonthInput{in(m(2026, 10), 500000, 0)}, ptr(m(2026, 9)))
	check(t, r[0], 0, 500000, 0, 500000, 0, "green")
}

func TestCompute_EmptyInput(t *testing.T) {
	if r := budgets.Compute(nil, nil); len(r) != 0 {
		t.Errorf("len = %d, want 0", len(r))
	}
}

func TestCompute_VaryingLimits(t *testing.T) {
	r := budgets.Compute([]budgets.MonthInput{in(m(2026, 9), lim, 600000), in(m(2026, 10), lim, 1000000), in(m(2026, 11), 1500000, 0)}, ptr(m(2026, 9)))
	check(t, r[0], 0, 1000000, 600000, 400000, 60, "green")
	check(t, r[1], 400000, 1400000, 1000000, 400000, 71, "green")
	check(t, r[2], 400000, 1900000, 0, 1900000, 0, "green")
}

func TestCompute_DoesNotMutateInput(t *testing.T) {
	src := []budgets.MonthInput{in(m(2026, 9), lim, 800000), in(m(2026, 10), lim, 0)}
	cp := append([]budgets.MonthInput(nil), src...)
	budgets.Compute(src, ptr(m(2026, 9)))
	for i := range src {
		if src[i] != cp[i] {
			t.Errorf("input[%d] mutated: %+v != %+v", i, src[i], cp[i])
		}
	}
}

func TestCompute_StatusBoundaries(t *testing.T) {
	cases := []struct {
		spent, pct, remaining int64
		status                string
	}{
		{0, 0, 1000000, "green"},
		{790000, 79, 210000, "green"},
		{799999, 79, 200001, "green"},
		{800000, 80, 200000, "amber"},
		{990000, 99, 10000, "amber"},
		{999999, 99, 1, "amber"},
		{1000000, 100, 0, "red"},
		{1250000, 125, -250000, "red"},
	}
	for _, c := range cases {
		r := budgets.Compute([]budgets.MonthInput{in(m(2026, 10), lim, c.spent)}, nil)
		check(t, r[0], 0, lim, c.spent, c.remaining, c.pct, c.status)
	}
}

func limits() []models.BudgetLimit {
	return []models.BudgetLimit{
		{EffectiveMonth: m(2026, 11), MonthlyLimit: 1500000},
		{EffectiveMonth: m(2026, 9), MonthlyLimit: 1000000},
	}
}

func checkLimit(t *testing.T, ls []models.BudgetLimit, mo time.Time, want int64, ok bool) {
	t.Helper()
	got, gotOK := budgets.LimitFor(ls, mo)
	if got != want || gotOK != ok {
		t.Errorf("LimitFor(%s) = (%d, %v), want (%d, %v)", mo.Format("2006-01"), got, gotOK, want, ok)
	}
}

func TestLimitFor_BeforeFirstEntry(t *testing.T) { checkLimit(t, limits(), m(2026, 8), 0, false) }
func TestLimitFor_ExactFirstEntry(t *testing.T)  { checkLimit(t, limits(), m(2026, 9), 1000000, true) }
func TestLimitFor_BetweenEntries(t *testing.T)   { checkLimit(t, limits(), m(2026, 10), 1000000, true) }
func TestLimitFor_ExactLaterEntry(t *testing.T)  { checkLimit(t, limits(), m(2026, 11), 1500000, true) }
func TestLimitFor_AfterLastEntry(t *testing.T)   { checkLimit(t, limits(), m(2026, 12), 1500000, true) }
func TestLimitFor_Empty(t *testing.T)            { checkLimit(t, nil, m(2026, 9), 0, false) }

func TestLimitFor_FeedsCompute(t *testing.T) {
	ls := []models.BudgetLimit{
		{EffectiveMonth: m(2026, 9), MonthlyLimit: 1000000},
		{EffectiveMonth: m(2026, 11), MonthlyLimit: 1500000},
	}
	var ins []budgets.MonthInput
	for _, mo := range []time.Month{9, 10, 11, 12} {
		l, _ := budgets.LimitFor(ls, m(2026, mo))
		ins = append(ins, in(m(2026, mo), l, 0))
	}
	r := budgets.Compute(ins, ptr(m(2026, 9)))
	wantLimit := []int64{1000000, 1000000, 1500000, 1500000}
	wantAvail := []int64{1000000, 2000000, 3500000, 5000000}
	for i := range r {
		if r[i].Limit != wantLimit[i] || r[i].Available != wantAvail[i] {
			t.Errorf("%s limit/avail = %d/%d, want %d/%d", r[i].Month.Format("2006-01"), r[i].Limit, r[i].Available, wantLimit[i], wantAvail[i])
		}
	}
}
