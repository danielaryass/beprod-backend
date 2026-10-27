package dashboard_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"

	"beprod/internal/models"
	"beprod/internal/testutil"
)

type block struct {
	Total int64
	Items []struct {
		CategoryID *string
		Name       string
		Amount     int64
		Percent    int64
	}
}

type dash struct {
	Month       string
	HasAccounts bool
	Balance     struct {
		Total    int64
		Accounts []struct {
			Name    string
			Balance int64
		}
	}
	Summary    struct{ Income, Expense, Net int64 }
	SpentToday int64
	Breakdown  struct{ Expense, Income block }
	Trend      []struct {
		Month           string
		Income, Expense int64
	}
	Budgets []struct {
		PercentUsed int64
		Category    struct{ Name string }
	}
	Due struct {
		Items []struct{ Note string }
		Total int64
	}
	Recent []struct {
		ID, Note, Source, Type string
		Amount                 int64
	}
}

func freeze(t *testing.T) { testutil.SetNow(t, time.Date(2026, 10, 20, 5, 0, 0, 0, time.UTC)) }

func seedAccount(t *testing.T, db *gorm.DB, userID uuid.UUID, name, typ string, opening int64) models.Account {
	t.Helper()
	var n int64
	db.Model(&models.Account{}).Where("user_id = ?", userID).Count(&n)
	acc := models.Account{UserID: userID, Name: name, Type: typ, OpeningBalance: opening,
		OpeningDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), SortOrder: int(n) + 1}
	if err := db.Create(&acc).Error; err != nil {
		t.Fatal(err)
	}
	return acc
}

func seedTx(t *testing.T, db *gorm.DB, tx models.Transaction) models.Transaction {
	t.Helper()
	if tx.Tags == nil {
		tx.Tags = pq.StringArray{}
	}
	if tx.Source == "" {
		tx.Source = models.SourceApp
	}
	if err := db.Create(&tx).Error; err != nil {
		t.Fatal(err)
	}
	return tx
}

func expenseCats(t *testing.T, db *gorm.DB, userID uuid.UUID) []models.Category {
	t.Helper()
	var cats []models.Category
	if err := db.Where("user_id = ? AND type = ?", userID, "expense").Order("sort_order").Find(&cats).Error; err != nil {
		t.Fatal(err)
	}
	return cats
}

func catByName(t *testing.T, db *gorm.DB, userID uuid.UUID, name string) models.Category {
	t.Helper()
	var c models.Category
	if err := db.Where("user_id = ? AND name = ?", userID, name).First(&c).Error; err != nil {
		t.Fatal(err)
	}
	return c
}

func at(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, time.UTC)
}

func getDash(t *testing.T, a *testutil.TestApp, cookie, query string) testutil.Response {
	t.Helper()
	return a.Do(t, "GET", "/api/v1/dashboard"+query, nil, cookie)
}

func decode(t *testing.T, r testutil.Response) dash {
	t.Helper()
	if r.Status != 200 {
		t.Fatalf("status %d body %s", r.Status, r.Raw)
	}
	var d dash
	r.Decode(t, &d)
	return d
}

func eq[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

// expenseTx seeds an Oct-2026 expense of amount on cat.
func expenseTx(t *testing.T, db *gorm.DB, u models.User, acc models.Account, cat models.Category, amount int64, when time.Time) models.Transaction {
	return seedTx(t, db, models.Transaction{UserID: u.ID, Type: models.TypeExpense, Amount: amount, OccurredAt: when, AccountID: acc.ID, CategoryID: &cat.ID})
}

func TestDashboard_RequiresSession(t *testing.T) {
	freeze(t)
	a := testutil.NewApp(t)
	r := getDash(t, a, "", "")
	eq(t, "status", r.Status, 401)
	eq(t, "code", r.ErrCode(), "UNAUTHENTICATED")
}

func TestDashboard_InvalidMonth(t *testing.T) {
	freeze(t)
	a := testutil.NewApp(t)
	_, c := a.NewUser(t, "a@x.com")
	for _, q := range []string{"?month=2026-13", "?month=abc", "?month=2026-1", "?month=2026-10-01"} {
		r := getDash(t, a, c, q)
		eq(t, q+" status", r.Status, 400)
		eq(t, q+" code", r.ErrCode(), "VALIDATION_ERROR")
		eq(t, q+" field", r.Field("month"), "Enter a valid month (YYYY-MM)")
	}
}

func TestDashboard_BalanceTotal(t *testing.T) {
	freeze(t)
	a := testutil.NewApp(t)
	u, c := a.NewUser(t, "a@x.com")
	seedAccount(t, a.DB, u.ID, "Cash", models.AccountCash, 300000)
	seedAccount(t, a.DB, u.ID, "BCA", models.AccountBank, 700000)
	seedAccount(t, a.DB, u.ID, "Credit card", models.AccountCreditCard, -200000)
	d := decode(t, getDash(t, a, c, ""))
	eq(t, "hasAccounts", d.HasAccounts, true)
	eq(t, "total", d.Balance.Total, int64(800000))
	eq(t, "accounts", len(d.Balance.Accounts), 3)
	for i, want := range []int64{300000, 700000, -200000} {
		eq(t, fmt.Sprintf("account %d", i), d.Balance.Accounts[i].Balance, want)
	}
}

func TestDashboard_ArchivedAccountExcludedFromTotal(t *testing.T) {
	freeze(t)
	a := testutil.NewApp(t)
	u, c := a.NewUser(t, "a@x.com")
	seedAccount(t, a.DB, u.ID, "Cash", models.AccountCash, 300000)
	old := seedAccount(t, a.DB, u.ID, "Old", models.AccountBank, 500000)
	a.DB.Model(&models.Account{}).Where("id = ?", old.ID).Update("status", models.StatusArchived)
	d := decode(t, getDash(t, a, c, ""))
	eq(t, "total", d.Balance.Total, int64(300000))
	eq(t, "accounts", len(d.Balance.Accounts), 1)
	eq(t, "name", d.Balance.Accounts[0].Name, "Cash")
}

func TestDashboard_NoAccounts(t *testing.T) {
	freeze(t)
	a := testutil.NewApp(t)
	_, c := a.NewUser(t, "a@x.com")
	r := getDash(t, a, c, "")
	d := decode(t, r)
	eq(t, "hasAccounts", d.HasAccounts, false)
	eq(t, "total", d.Balance.Total, int64(0))
	accs, ok := r.Body["balance"].(map[string]any)["accounts"].([]any)
	if !ok || len(accs) != 0 {
		t.Fatalf("balance.accounts = %v, want []", r.Body["balance"])
	}
}

func TestDashboard_SummaryAndBreakdown(t *testing.T) {
	freeze(t)
	a := testutil.NewApp(t)
	u, c := a.NewUser(t, "a@x.com")
	acc := seedAccount(t, a.DB, u.ID, "Cash", models.AccountCash, 0)
	sal := catByName(t, a.DB, u.ID, "Salary")
	seedTx(t, a.DB, models.Transaction{UserID: u.ID, Type: models.TypeIncome, Amount: 10000000, OccurredAt: at(2026, 10, 5, 5, 0), AccountID: acc.ID, CategoryID: &sal.ID})
	expenseTx(t, a.DB, u, acc, catByName(t, a.DB, u.ID, "Food & Drinks"), 3000000, at(2026, 10, 6, 5, 0))
	expenseTx(t, a.DB, u, acc, catByName(t, a.DB, u.ID, "Transport"), 1500000, at(2026, 10, 7, 5, 0))
	expenseTx(t, a.DB, u, acc, catByName(t, a.DB, u.ID, "Shopping"), 1500000, at(2026, 10, 8, 5, 0))
	d := decode(t, getDash(t, a, c, ""))
	eq(t, "income", d.Summary.Income, int64(10000000))
	eq(t, "expense", d.Summary.Expense, int64(6000000))
	eq(t, "net", d.Summary.Net, int64(4000000))
	e := d.Breakdown.Expense
	eq(t, "exp total", e.Total, int64(6000000))
	eq(t, "exp items", len(e.Items), 3)
	for i, w := range []struct {
		n    string
		a, p int64
	}{{"Food & Drinks", 3000000, 50}, {"Shopping", 1500000, 25}, {"Transport", 1500000, 25}} {
		eq(t, "name", e.Items[i].Name, w.n)
		eq(t, w.n+" amount", e.Items[i].Amount, w.a)
		eq(t, w.n+" percent", e.Items[i].Percent, w.p)
	}
	in := d.Breakdown.Income
	eq(t, "inc total", in.Total, int64(10000000))
	eq(t, "inc items", len(in.Items), 1)
	eq(t, "inc name", in.Items[0].Name, "Salary")
	eq(t, "inc percent", in.Items[0].Percent, int64(100))
}

func TestDashboard_TransfersExcluded(t *testing.T) {
	freeze(t)
	a := testutil.NewApp(t)
	u, c := a.NewUser(t, "a@x.com")
	cash := seedAccount(t, a.DB, u.ID, "Cash", models.AccountCash, 300000)
	bca := seedAccount(t, a.DB, u.ID, "BCA", models.AccountBank, 700000)
	seedTx(t, a.DB, models.Transaction{UserID: u.ID, Type: models.TypeTransfer, Amount: 100000, OccurredAt: at(2026, 10, 10, 5, 0), AccountID: cash.ID, ToAccountID: &bca.ID})
	expenseTx(t, a.DB, u, cash, catByName(t, a.DB, u.ID, "Food & Drinks"), 20000, at(2026, 10, 11, 5, 0))
	d := decode(t, getDash(t, a, c, ""))
	eq(t, "expense", d.Summary.Expense, int64(20000))
	eq(t, "income", d.Summary.Income, int64(0))
	eq(t, "exp total", d.Breakdown.Expense.Total, int64(20000))
	eq(t, "inc items", len(d.Breakdown.Income.Items), 0)
	eq(t, "trend exp", d.Trend[11].Expense, int64(20000))
	eq(t, "trend inc", d.Trend[11].Income, int64(0))
	eq(t, "balance", d.Balance.Total, int64(980000))
}

func TestDashboard_EmptyMonth(t *testing.T) {
	freeze(t)
	a := testutil.NewApp(t)
	u, c := a.NewUser(t, "a@x.com")
	seedAccount(t, a.DB, u.ID, "Cash", models.AccountCash, 0)
	r := getDash(t, a, c, "")
	d := decode(t, r)
	eq(t, "summary", d.Summary, struct{ Income, Expense, Net int64 }{})
	eq(t, "exp total", d.Breakdown.Expense.Total, int64(0))
	eq(t, "exp items", len(d.Breakdown.Expense.Items), 0)
	eq(t, "inc items", len(d.Breakdown.Income.Items), 0)
	eq(t, "trend", len(d.Trend), 12)
	for _, p := range d.Trend {
		if p.Income != 0 || p.Expense != 0 {
			t.Fatalf("trend not zero: %+v", p)
		}
	}
	eq(t, "budgets", len(d.Budgets), 0)
	eq(t, "due items", len(d.Due.Items), 0)
	eq(t, "due total", d.Due.Total, int64(0))
	eq(t, "recent", len(d.Recent), 0)
	raw := string(r.Raw)
	eq(t, "items null", strings.Contains(raw, `"items":null`), false)
	eq(t, "recent null", strings.Contains(raw, `"recent":null`), false)
	eq(t, "budgets null", strings.Contains(raw, `"budgets":null`), false)
}

func othersSetup(t *testing.T, n int) dash {
	freeze(t)
	a := testutil.NewApp(t)
	u, c := a.NewUser(t, "a@x.com")
	acc := seedAccount(t, a.DB, u.ID, "Cash", models.AccountCash, 0)
	cats := expenseCats(t, a.DB, u.ID)
	amounts := []int64{80000, 70000, 60000, 50000, 40000, 30000, 20000, 10000}
	for i := 0; i < n; i++ {
		expenseTx(t, a.DB, u, acc, cats[i], amounts[i], at(2026, 10, 2+i, 5, 0))
	}
	return decode(t, getDash(t, a, c, ""))
}

func TestDashboard_OthersBucket(t *testing.T) {
	e := othersSetup(t, 8).Breakdown.Expense
	eq(t, "items", len(e.Items), 7)
	for i, w := range []int64{80000, 70000, 60000, 50000, 40000, 30000} {
		eq(t, fmt.Sprintf("item %d", i), e.Items[i].Amount, w)
	}
	o := e.Items[6]
	eq(t, "others name", o.Name, "Others")
	if o.CategoryID != nil {
		t.Fatalf("others categoryId = %v, want nil", *o.CategoryID)
	}
	eq(t, "others amount", o.Amount, int64(30000))
	eq(t, "total", e.Total, int64(360000))
	eq(t, "first percent", e.Items[0].Percent, int64(22))
	eq(t, "others percent", o.Percent, int64(8))
}

func TestDashboard_ExactlySixCategoriesNoOthers(t *testing.T) {
	e := othersSetup(t, 6).Breakdown.Expense
	eq(t, "items", len(e.Items), 6)
	for _, it := range e.Items {
		eq(t, "others", it.Name == "Others", false)
	}
}

func TestDashboard_ArchivedCategoryStillInBreakdown(t *testing.T) {
	freeze(t)
	a := testutil.NewApp(t)
	u, c := a.NewUser(t, "a@x.com")
	acc := seedAccount(t, a.DB, u.ID, "Cash", models.AccountCash, 0)
	food := catByName(t, a.DB, u.ID, "Food & Drinks")
	expenseTx(t, a.DB, u, acc, food, 12345, at(2026, 10, 3, 5, 0))
	a.DB.Model(&models.Category{}).Where("id = ?", food.ID).Update("status", models.StatusArchived)
	e := decode(t, getDash(t, a, c, "")).Breakdown.Expense
	eq(t, "items", len(e.Items), 1)
	eq(t, "name", e.Items[0].Name, "Food & Drinks")
	eq(t, "amount", e.Items[0].Amount, int64(12345))
}

func TestDashboard_Trend12MonthsYearBoundary(t *testing.T) {
	freeze(t)
	a := testutil.NewApp(t)
	u, c := a.NewUser(t, "a@x.com")
	acc := seedAccount(t, a.DB, u.ID, "Cash", models.AccountCash, 0)
	food := catByName(t, a.DB, u.ID, "Food & Drinks")
	sal := catByName(t, a.DB, u.ID, "Salary")
	seedTx(t, a.DB, models.Transaction{UserID: u.ID, Type: models.TypeIncome, Amount: 1000, OccurredAt: at(2025, 12, 15, 5, 0), AccountID: acc.ID, CategoryID: &sal.ID})
	expenseTx(t, a.DB, u, acc, food, 500, at(2026, 2, 10, 5, 0))
	expenseTx(t, a.DB, u, acc, food, 700, at(2025, 12, 31, 17, 30))
	expenseTx(t, a.DB, u, acc, food, 300, at(2026, 1, 31, 17, 30))
	expenseTx(t, a.DB, u, acc, food, 999, at(2025, 2, 15, 5, 0))
	expenseTx(t, a.DB, u, acc, food, 888, at(2026, 3, 5, 5, 0))
	d := decode(t, getDash(t, a, c, "?month=2026-02"))
	eq(t, "month", d.Month, "2026-02")
	eq(t, "len", len(d.Trend), 12)
	eq(t, "first", d.Trend[0].Month, "2025-03")
	eq(t, "last", d.Trend[11].Month, "2026-02")
	eq(t, "idx9", d.Trend[9].Month, "2025-12")
	eq(t, "idx10", d.Trend[10].Month, "2026-01")
	eq(t, "t9 income", d.Trend[9].Income, int64(1000))
	eq(t, "t9 expense", d.Trend[9].Expense, int64(0))
	eq(t, "t10 expense", d.Trend[10].Expense, int64(700))
	eq(t, "t11 expense", d.Trend[11].Expense, int64(800))
	for i, p := range d.Trend {
		if i < 9 && (p.Income != 0 || p.Expense != 0) {
			t.Fatalf("trend[%d] = %+v, want zero", i, p)
		}
	}
	eq(t, "summary expense", d.Summary.Expense, int64(800))
}

func TestDashboard_DefaultMonthIsCurrent(t *testing.T) {
	freeze(t)
	a := testutil.NewApp(t)
	u, c := a.NewUser(t, "a@x.com")
	acc := seedAccount(t, a.DB, u.ID, "Cash", models.AccountCash, 0)
	expenseTx(t, a.DB, u, acc, catByName(t, a.DB, u.ID, "Food & Drinks"), 5000, at(2026, 9, 15, 5, 0))
	d := decode(t, getDash(t, a, c, ""))
	eq(t, "month", d.Month, "2026-10")
	eq(t, "trend last", d.Trend[11].Month, "2026-10")
	eq(t, "summary", d.Summary.Expense, int64(0))
}

func TestDashboard_BudgetsTop5ByPercent(t *testing.T) {
	freeze(t)
	a := testutil.NewApp(t)
	u, c := a.NewUser(t, "a@x.com")
	acc := seedAccount(t, a.DB, u.ID, "Cash", models.AccountCash, 0)
	cats := expenseCats(t, a.DB, u.ID)
	for i := 0; i < 6; i++ {
		r := a.Do(t, "POST", "/api/v1/budgets", map[string]any{"categoryId": cats[i].ID.String(), "monthlyLimit": 100000, "rolloverEnabled": false}, c)
		eq(t, "create status", r.Status, 201)
		expenseTx(t, a.DB, u, acc, cats[i], int64(i+1)*10000, at(2026, 10, 2+i, 5, 0))
	}
	d := decode(t, getDash(t, a, c, ""))
	eq(t, "budgets", len(d.Budgets), 5)
	for i, w := range []int64{60, 50, 40, 30, 20} {
		eq(t, fmt.Sprintf("budget %d", i), d.Budgets[i].PercentUsed, w)
	}
}

func TestDashboard_DueShowsFiveOldestWithTotal(t *testing.T) {
	freeze(t)
	a := testutil.NewApp(t)
	u, c := a.NewUser(t, "a@x.com")
	acc := seedAccount(t, a.DB, u.ID, "Cash", models.AccountCash, 100000)
	cat := catByName(t, a.DB, u.ID, "Food & Drinks")
	rule := uuid.New()
	occ := func(rule uuid.UUID, d int, status, note string) {
		o := models.RecurringOccurrence{RuleID: rule, UserID: u.ID, DueDate: time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC), Status: status,
			Type: models.TypeExpense, Amount: 1000, AccountID: acc.ID, CategoryID: &cat.ID, Tags: pq.StringArray{}, Note: note}
		if err := a.DB.Create(&o).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []int{7, 3, 5, 1, 6, 2, 4} {
		occ(rule, d, models.OccPending, fmt.Sprintf("due%d", d))
	}
	occ(uuid.New(), 1, models.OccConfirmed, "done")
	d := decode(t, getDash(t, a, c, ""))
	eq(t, "due total", d.Due.Total, int64(7))
	eq(t, "due items", len(d.Due.Items), 5)
	for i, it := range d.Due.Items {
		eq(t, "note", it.Note, fmt.Sprintf("due%d", i+1))
	}
	eq(t, "balance", d.Balance.Total, int64(100000))
}

func TestDashboard_RecentFiveNewest(t *testing.T) {
	freeze(t)
	a := testutil.NewApp(t)
	u, c := a.NewUser(t, "a@x.com")
	acc := seedAccount(t, a.DB, u.ID, "Cash", models.AccountCash, 0)
	cat := catByName(t, a.DB, u.ID, "Food & Drinks")
	for d := 1; d <= 7; d++ {
		seedTx(t, a.DB, models.Transaction{UserID: u.ID, Type: models.TypeExpense, Amount: 1000, OccurredAt: at(2026, 10, d, 5, 0), AccountID: acc.ID, CategoryID: &cat.ID, Note: fmt.Sprintf("n%d", d)})
	}
	seedTx(t, a.DB, models.Transaction{UserID: u.ID, Type: models.TypeExpense, Amount: 1000, OccurredAt: at(2026, 9, 30, 5, 0), AccountID: acc.ID, CategoryID: &cat.ID, Note: "old"})
	d := decode(t, getDash(t, a, c, ""))
	eq(t, "recent", len(d.Recent), 5)
	for i, w := range []string{"n7", "n6", "n5", "n4", "n3"} {
		eq(t, "note", d.Recent[i].Note, w)
	}
}

func TestDashboard_QuickAddTransactionIncluded(t *testing.T) {
	freeze(t)
	a := testutil.NewApp(t)
	u, c := a.NewUser(t, "a@x.com")
	acc := seedAccount(t, a.DB, u.ID, "Cash", models.AccountCash, 100000)
	food := catByName(t, a.DB, u.ID, "Food & Drinks")
	seedTx(t, a.DB, models.Transaction{UserID: u.ID, Type: models.TypeExpense, Amount: 25000, OccurredAt: at(2026, 10, 5, 5, 0), AccountID: acc.ID, CategoryID: &food.ID, Source: models.SourceQuickAdd})
	d := decode(t, getDash(t, a, c, ""))
	eq(t, "expense", d.Summary.Expense, int64(25000))
	eq(t, "recent source", d.Recent[0].Source, "quick_add")
	eq(t, "trend", d.Trend[11].Expense, int64(25000))
	eq(t, "breakdown", d.Breakdown.Expense.Items[0].Name, "Food & Drinks")
	eq(t, "balance", d.Balance.Total, int64(75000))
}

func TestDashboard_OtherUserDataNeverAppears(t *testing.T) {
	freeze(t)
	a := testutil.NewApp(t)
	ua, ca := a.NewUser(t, "a@x.com")
	ub, cb := a.NewUser(t, "b@x.com")
	seedAccount(t, a.DB, ua.ID, "A Cash", models.AccountCash, 1000)
	bank := seedAccount(t, a.DB, ub.ID, "B Bank", models.AccountBank, 5000000)
	food := catByName(t, a.DB, ub.ID, "Food & Drinks")
	expenseTx(t, a.DB, ub, bank, food, 123456, at(2026, 10, 5, 5, 0))
	r := a.Do(t, "POST", "/api/v1/budgets", map[string]any{"categoryId": food.ID.String(), "monthlyLimit": 100000, "rolloverEnabled": false}, cb)
	eq(t, "budget status", r.Status, 201)

	r = getDash(t, a, ca, "")
	d := decode(t, r)
	eq(t, "A total", d.Balance.Total, int64(1000))
	eq(t, "A accounts", len(d.Balance.Accounts), 1)
	eq(t, "A expense", d.Summary.Expense, int64(0))
	eq(t, "A recent", len(d.Recent), 0)
	eq(t, "A budgets", len(d.Budgets), 0)
	eq(t, "leak", strings.Contains(string(r.Raw), "B Bank"), false)

	d = decode(t, getDash(t, a, cb, ""))
	eq(t, "B total", d.Balance.Total, int64(5000000-123456))
	eq(t, "B budgets", len(d.Budgets), 1)
}

func TestDashboard_SpentToday(t *testing.T) {
	freeze(t) // 2026-10-20 12:00 Jakarta
	a := testutil.NewApp(t)
	u, c := a.NewUser(t, "a@x.com")
	acc := seedAccount(t, a.DB, u.ID, "Cash", models.AccountCash, 0)
	food := catByName(t, a.DB, u.ID, "Food & Drinks")
	sal := catByName(t, a.DB, u.ID, "Salary")
	expenseTx(t, a.DB, u, acc, food, 25000, at(2026, 10, 20, 1, 0))
	expenseTx(t, a.DB, u, acc, food, 10000, at(2026, 10, 19, 17, 30)) // 00:30 Jakarta today
	expenseTx(t, a.DB, u, acc, food, 99000, at(2026, 10, 19, 16, 30)) // 23:30 Jakarta yesterday
	seedTx(t, a.DB, models.Transaction{UserID: u.ID, Type: models.TypeIncome, Amount: 500000, OccurredAt: at(2026, 10, 20, 2, 0), AccountID: acc.ID, CategoryID: &sal.ID})
	eq(t, "spentToday", decode(t, getDash(t, a, c, "")).SpentToday, int64(35000))
	eq(t, "spentToday on a past month", decode(t, getDash(t, a, c, "?month=2026-09")).SpentToday, int64(35000))
}
