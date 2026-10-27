package plan_test

import (
	"testing"
	"time"

	"github.com/lib/pq"

	"beprod/internal/jakarta"
	"beprod/internal/models"
	"beprod/internal/testutil"
)

func wib(y int, m time.Month, d, h int) time.Time { return time.Date(y, m, d, h, 0, 0, 0, jakarta.Loc) }
func day(y int, m time.Month, d int) time.Time    { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

type env struct {
	t      *testing.T
	a      *testutil.TestApp
	user   models.User
	cookie string
}

func setup(t *testing.T) env {
	t.Helper()
	testutil.SetNow(t, wib(2026, 10, 20, 10))
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u@example.com")
	return env{t, a, u, cookie}
}

func (e env) account(name, typ string, opening int64) models.Account {
	e.t.Helper()
	acc := models.Account{UserID: e.user.ID, Name: name, Type: typ, OpeningBalance: opening, OpeningDate: day(2026, 9, 1), Status: models.StatusActive}
	if err := e.a.DB.Create(&acc).Error; err != nil {
		e.t.Fatal(err)
	}
	return acc
}

func (e env) expense(acc models.Account, amount int64, at time.Time) {
	e.t.Helper()
	tx := models.Transaction{UserID: e.user.ID, Type: models.TypeExpense, Amount: amount, OccurredAt: at, AccountID: acc.ID,
		Tags: pq.StringArray{}, Source: models.SourceApp}
	if err := e.a.DB.Create(&tx).Error; err != nil {
		e.t.Fatal(err)
	}
}

func (e env) rule(acc models.Account, typ, freq string, amount int64, next time.Time) models.RecurringRule {
	e.t.Helper()
	r := models.RecurringRule{UserID: e.user.ID, Type: typ, Amount: amount, AccountID: acc.ID, Tags: pq.StringArray{}, Frequency: freq,
		StartDate: next, NextDueDate: next, Status: models.StatusActive}
	if err := e.a.DB.Create(&r).Error; err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e env) get() map[string]any {
	e.t.Helper()
	r := e.a.Do(e.t, "GET", "/api/v1/plan", nil, e.cookie)
	if r.Status != 200 {
		e.t.Fatalf("GET /plan: %d %s", r.Status, r.Raw)
	}
	return r.Body
}

func check(t *testing.T, got map[string]any, want map[string]any) {
	t.Helper()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}

func TestPlan_SafeToSpend(t *testing.T) {
	e := setup(t)
	bank := e.account("Bank", models.AccountBank, 5_000_000)
	e.account("Cash", models.AccountCash, 500_000)
	e.account("Card", models.AccountCreditCard, -1_200_000)
	pocket := e.account("Pocket", models.AccountBank, 2_000_000)
	r := e.a.Do(t, "POST", "/api/v1/goals", map[string]any{"name": "Trip", "targetAmount": 2_600_000, "targetDate": "2026-11-15", "accountId": pocket.ID}, e.cookie)
	if r.Status != 201 {
		t.Fatalf("goal: %d %s", r.Status, r.Raw)
	}
	old := e.rule(bank, models.TypeExpense, models.FreqMonthly, 100_000, day(2026, 10, 18))
	occ := models.RecurringOccurrence{RuleID: old.ID, UserID: e.user.ID, DueDate: day(2026, 10, 18), Status: models.OccPending,
		Type: models.TypeExpense, Amount: 100_000, AccountID: bank.ID, Tags: pq.StringArray{}}
	if err := e.a.DB.Create(&occ).Error; err != nil {
		t.Fatal(err)
	}
	// the rule's next due date has moved on; the pending 10-18 occurrence is the stored one
	e.a.DB.Model(&old).Update("next_due_date", day(2026, 11, 18))
	e.rule(bank, models.TypeExpense, models.FreqWeekly, 50_000, day(2026, 10, 21))
	e.rule(pocket, models.TypeExpense, models.FreqMonthly, 700_000, day(2026, 10, 22))
	e.rule(bank, models.TypeIncome, models.FreqMonthly, 9_000_000, day(2026, 10, 24))

	s := e.get()["safeToSpend"].(map[string]any)
	check(t, s, map[string]any{"paydayDay": 25.0, "nextPayday": "2026-10-25", "days": 5.0, "spendable": 5_500_000.0,
		"cardDebt": 1_200_000.0, "bills": 150_000.0, "goals": 300_000.0, "safe": 3_850_000.0, "perDay": 770_000.0})
}

func TestPlan_NegativeSafe(t *testing.T) {
	e := setup(t)
	e.account("Cash", models.AccountCash, 100_000)
	e.account("Card", models.AccountCreditCard, -500_000)
	s := e.get()["safeToSpend"].(map[string]any)
	check(t, s, map[string]any{"safe": -400_000.0, "perDay": nil})
}

func TestPlan_OverpaidCardIsNotMoney(t *testing.T) {
	e := setup(t)
	e.account("Cash", models.AccountCash, 1_000_000)
	e.account("Card", models.AccountCreditCard, 300_000)
	s := e.get()["safeToSpend"].(map[string]any)
	check(t, s, map[string]any{"cardDebt": 0.0, "spendable": 1_000_000.0, "safe": 1_000_000.0})
}

func TestPlan_Emergency(t *testing.T) {
	e := setup(t)
	bank := e.account("Bank", models.AccountBank, 5_000_000)
	card := e.account("Card", models.AccountCreditCard, 0)
	pocket := e.account("Pocket", models.AccountBank, 6_000_000)
	e.a.Do(t, "POST", "/api/v1/goals", map[string]any{"name": "Trip", "targetAmount": 9_000_000, "accountId": pocket.ID}, e.cookie)
	e.expense(bank, 900_000, wib(2026, 8, 15, 12))
	e.expense(card, 1_200_000, wib(2026, 9, 10, 12))
	e.expense(bank, 5_000_000, wib(2026, 10, 5, 12))

	em := e.get()["emergency"].(map[string]any)
	// liquid: bank 5M−0.9M−5M + pocket 6M = 5.1M; card owes 1.2M; have 3.9M
	check(t, em, map[string]any{"have": 3_900_000.0, "monthlySpend": 1_050_000.0, "months": 3.7, "level": "mid",
		"needFor3": 0.0, "needFor6": 2_400_000.0})
}

func TestPlan_EmptyUser(t *testing.T) {
	e := setup(t)
	b := e.get()
	check(t, b["safeToSpend"].(map[string]any), map[string]any{"safe": 0.0, "perDay": 0.0, "days": 5.0})
	check(t, b["emergency"].(map[string]any), map[string]any{"monthlySpend": nil, "months": nil, "level": nil, "needFor3": 0.0, "needFor6": 0.0})
}
