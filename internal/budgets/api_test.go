package budgets_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"beprod/internal/budgets"
	"beprod/internal/jakarta"
	"beprod/internal/models"
	"beprod/internal/testutil"
)

func wib(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, jakarta.Loc)
}

type listResp struct {
	Month    string                `json:"month"`
	Budgets  []budgets.View        `json:"budgets"`
	Inactive []budgets.InactiveDTO `json:"inactive"`
}
type env struct {
	a      *testutil.TestApp
	user   models.User
	cookie string
	acct   models.Account // akun aktif "BCA"
}

func newEnv(t *testing.T) *env {
	t.Helper()
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "a@example.com")
	acct := models.Account{UserID: u.ID, Name: "BCA", Type: models.AccountBank,
		OpeningDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Status: models.StatusActive}
	if err := a.DB.Create(&acct).Error; err != nil {
		t.Fatal(err)
	}
	return &env{a, u, cookie, acct}
}

func (e *env) cat(t *testing.T, uid uuid.UUID, name, typ string) models.Category {
	t.Helper()
	var c models.Category
	if err := e.a.DB.Where("user_id = ? AND name = ? AND type = ?", uid, name, typ).First(&c).Error; err != nil {
		t.Fatal(err)
	}
	return c
}

func (e *env) food(t *testing.T) models.Category {
	return e.cat(t, e.user.ID, "Food & Drinks", models.TypeExpense)
}

func (e *env) addTx(t *testing.T, typ string, amount int64, catID *uuid.UUID, accountID uuid.UUID, at time.Time) {
	t.Helper()
	tx := models.Transaction{UserID: e.user.ID, Type: typ, Amount: amount, OccurredAt: at, AccountID: accountID,
		CategoryID: catID, Tags: pq.StringArray{}, Source: models.SourceApp}
	if err := e.a.DB.Create(&tx).Error; err != nil {
		t.Fatal(err)
	}
}

func (e *env) spend(t *testing.T, cat models.Category, amount int64, at time.Time) {
	t.Helper()
	e.addTx(t, models.TypeExpense, amount, &cat.ID, e.acct.ID, at)
}

func (e *env) create(t *testing.T, catID uuid.UUID, limit int64, rollover bool) budgets.View {
	t.Helper()
	r := e.a.Do(t, "POST", "/api/v1/budgets", map[string]any{"categoryId": catID.String(), "monthlyLimit": limit, "rolloverEnabled": rollover}, e.cookie)
	if r.Status != 201 {
		t.Fatalf("create: status %d body %s", r.Status, r.Raw)
	}
	var out struct {
		Budget budgets.View `json:"budget"`
	}
	r.Decode(t, &out)
	return out.Budget
}

func (e *env) list(t *testing.T, month string) listResp {
	t.Helper()
	path := "/api/v1/budgets"
	if month != "" {
		path += "?month=" + month
	}
	r := e.a.Do(t, "GET", path, nil, e.cookie)
	if r.Status != 200 {
		t.Fatalf("list: status %d body %s", r.Status, r.Raw)
	}
	var out listResp
	r.Decode(t, &out)
	return out
}

func (e *env) patch(t *testing.T, id uuid.UUID, body map[string]any) budgetResp {
	t.Helper()
	r := e.a.Do(t, "PATCH", "/api/v1/budgets/"+id.String(), body, e.cookie)
	if r.Status != 200 {
		t.Fatalf("patch: status %d body %s", r.Status, r.Raw)
	}
	var out budgetResp
	r.Decode(t, &out)
	return out
}

type budgetResp struct {
	Budget budgets.View `json:"budget"`
}

func (e *env) reload(t *testing.T, id uuid.UUID) models.Budget {
	t.Helper()
	var b models.Budget
	if err := e.a.DB.First(&b, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	return b
}

func eq(t *testing.T, name string, got, want int64) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %d, want %d", name, got, want)
	}
}

func eqS(t *testing.T, name, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q, want %q", name, got, want)
	}
}

// nums asserts carryover, available, spent, remaining.
func nums(t *testing.T, v budgets.View, carry, avail, spent, remaining int64) {
	t.Helper()
	eq(t, "carryover", v.Carryover, carry)
	eq(t, "available", v.Available, avail)
	eq(t, "spent", v.Spent, spent)
	eq(t, "remaining", v.Remaining, remaining)
}

func one(t *testing.T, l listResp) budgets.View {
	t.Helper()
	if len(l.Budgets) != 1 {
		t.Fatalf("budgets len = %d, want 1", len(l.Budgets))
	}
	return l.Budgets[0]
}

func TestBudgets_US71_AC1_CreateShowsZeroSpent(t *testing.T) {
	e := newEnv(t)
	testutil.SetNow(t, wib(2026, 10, 15, 12, 0))
	v := e.create(t, e.food(t).ID, 2000000, false)
	eq(t, "monthlyLimit", v.MonthlyLimit, 2000000)
	eq(t, "currentLimit", v.CurrentLimit, 2000000)
	eq(t, "percentUsed", v.PercentUsed, 0)
	nums(t, v, 0, 2000000, 0, 2000000)
	eqS(t, "status", v.Status, "green")
	eqS(t, "category", v.Category.Name, "Food & Drinks")
	if v.RolloverEnabled || v.Category.Archived {
		t.Error("rolloverEnabled/archived should be false")
	}
	r := e.a.Do(t, "GET", "/api/v1/budgets", nil, e.cookie)
	if r.Status != 200 {
		t.Fatalf("status %d", r.Status)
	}
	eqS(t, "month", r.Body["month"].(string), "2026-10")
	if _, ok := r.Body["inactive"].([]any); !ok {
		t.Errorf("inactive should be an array: %s", r.Raw)
	}
	l := e.list(t, "")
	got := one(t, l)
	nums(t, got, 0, 2000000, 0, 2000000)
	if len(l.Inactive) != 0 {
		t.Error("inactive should be empty")
	}
}

func TestBudgets_US71_AC2_ZeroLimitRejected(t *testing.T) {
	e := newEnv(t)
	testutil.SetNow(t, wib(2026, 10, 15, 12, 0))
	food := e.food(t)
	for limit, want := range map[int64]string{0: "Budget must be greater than 0.", -5: "Budget must be greater than 0.", 1000000000000: "Budget is too large"} {
		r := e.a.Do(t, "POST", "/api/v1/budgets", map[string]any{"categoryId": food.ID.String(), "monthlyLimit": limit}, e.cookie)
		eq(t, "status", int64(r.Status), 400)
		eqS(t, "code", r.ErrCode(), "VALIDATION_ERROR")
		eqS(t, "field", r.Field("monthlyLimit"), want)
	}
	var n int64
	e.a.DB.Model(&models.Budget{}).Count(&n)
	eq(t, "budgets count", n, 0)
}

// rolloverSetup: Food 1,000,000 created in Sept, spent in Sept, now Oct 1 00:30.
func rolloverSetup(t *testing.T, rollover bool, spent int64) (*env, budgets.View) {
	e := newEnv(t)
	testutil.SetNow(t, wib(2026, 9, 15, 12, 0))
	food := e.food(t)
	v := e.create(t, food.ID, 1000000, rollover)
	e.spend(t, food, spent, wib(2026, 9, 12, 12, 0))
	testutil.SetNow(t, wib(2026, 10, 1, 0, 30))
	return e, v
}

func TestBudgets_US72_AC1_RolloverCarriesUnused(t *testing.T) {
	e, _ := rolloverSetup(t, true, 800000)
	oct := one(t, e.list(t, "2026-10"))
	nums(t, oct, 200000, 1200000, 0, 1200000)
	eq(t, "monthlyLimit", oct.MonthlyLimit, 1000000)
	sep := one(t, e.list(t, "2026-09"))
	nums(t, sep, 0, 1000000, 800000, 200000)
	eq(t, "percentUsed", sep.PercentUsed, 80)
	eqS(t, "status", sep.Status, "amber")
}

func TestBudgets_US72_AC2_OverspendNotCarried(t *testing.T) {
	e, _ := rolloverSetup(t, true, 1200000)
	nums(t, one(t, e.list(t, "2026-10")), 0, 1000000, 0, 1000000)
	sep := one(t, e.list(t, "2026-09"))
	eq(t, "remaining", sep.Remaining, -200000)
	eq(t, "percentUsed", sep.PercentUsed, 120)
	eqS(t, "status", sep.Status, "red")
}

func TestBudgets_US72_AC3_RolloverOffNoCarry(t *testing.T) {
	e, _ := rolloverSetup(t, false, 800000)
	nums(t, one(t, e.list(t, "2026-10")), 0, 1000000, 0, 1000000)
}

func TestBudgets_SpentRulesAndBoundaries(t *testing.T) {
	e := newEnv(t)
	testutil.SetNow(t, wib(2026, 10, 15, 12, 0))
	old := models.Account{UserID: e.user.ID, Name: "Old", Type: models.AccountBank,
		OpeningDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Status: models.StatusArchived}
	if err := e.a.DB.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	food := e.food(t)
	transport := e.cat(t, e.user.ID, "Transport", models.TypeExpense)
	e.create(t, food.ID, 1000000, false)
	e.addTx(t, models.TypeExpense, 100000, &food.ID, e.acct.ID, wib(2026, 10, 3, 10, 0))
	e.addTx(t, models.TypeExpense, 50000, &food.ID, old.ID, wib(2026, 10, 4, 10, 0))
	e.spend(t, food, 1000, wib(2026, 10, 1, 0, 0))
	e.spend(t, food, 2000, wib(2026, 10, 31, 23, 59))
	e.spend(t, food, 4000, wib(2026, 9, 30, 23, 59))
	e.spend(t, food, 8000, wib(2026, 11, 1, 0, 0))
	e.addTx(t, models.TypeIncome, 999000, &food.ID, e.acct.ID, wib(2026, 10, 5, 10, 0))
	tr := models.Transaction{UserID: e.user.ID, Type: models.TypeTransfer, Amount: 777000, OccurredAt: wib(2026, 10, 5, 11, 0),
		AccountID: e.acct.ID, ToAccountID: &old.ID, Tags: pq.StringArray{}, Source: models.SourceApp}
	if err := e.a.DB.Create(&tr).Error; err != nil {
		t.Fatal(err)
	}
	e.spend(t, transport, 30000, wib(2026, 10, 6, 10, 0))
	v := one(t, e.list(t, "2026-10"))
	nums(t, v, 0, 1000000, 153000, 847000)
	eq(t, "percentUsed", v.PercentUsed, 15)
}

func TestBudgets_CreatedMidMonthCountsWholeMonth(t *testing.T) {
	e := newEnv(t)
	testutil.SetNow(t, wib(2026, 10, 20, 12, 0))
	food := e.food(t)
	e.spend(t, food, 400000, wib(2026, 10, 2, 9, 0))
	v := e.create(t, food.ID, 1000000, false)
	nums(t, v, 0, 1000000, 400000, 600000)
	eq(t, "percentUsed", v.PercentUsed, 40)
}

func TestBudgets_MonthBeforeCreationOmitted(t *testing.T) {
	e := newEnv(t)
	testutil.SetNow(t, wib(2026, 10, 20, 12, 0))
	e.create(t, e.food(t).ID, 1000000, false)
	r := e.a.Do(t, "GET", "/api/v1/budgets?month=2026-09", nil, e.cookie)
	eqS(t, "month", r.Body["month"].(string), "2026-09")
	if b, ok := r.Body["budgets"].([]any); !ok || len(b) != 0 {
		t.Errorf("budgets should be []: %s", r.Raw)
	}
	one(t, e.list(t, "2026-10"))
}

func TestBudgets_LimitChangeDoesNotRewriteHistory(t *testing.T) {
	e := newEnv(t)
	testutil.SetNow(t, wib(2026, 10, 10, 12, 0))
	b := e.create(t, e.food(t).ID, 1000000, false)
	testutil.SetNow(t, wib(2026, 11, 5, 12, 0))
	v := e.patch(t, b.ID, map[string]any{"monthlyLimit": 1500000}).Budget
	eq(t, "monthlyLimit", v.MonthlyLimit, 1500000)
	eq(t, "currentLimit", v.CurrentLimit, 1500000)
	eq(t, "available", v.Available, 1500000)
	oct := one(t, e.list(t, "2026-10"))
	eq(t, "oct monthlyLimit", oct.MonthlyLimit, 1000000)
	eq(t, "oct currentLimit", oct.CurrentLimit, 1500000)
	eq(t, "oct available", oct.Available, 1000000)
	eq(t, "nov monthlyLimit", one(t, e.list(t, "2026-11")).MonthlyLimit, 1500000)
	var n int64
	e.a.DB.Model(&models.BudgetLimit{}).Where("budget_id = ?", b.ID).Count(&n)
	eq(t, "limit rows", n, 2)
	e.patch(t, b.ID, map[string]any{"monthlyLimit": 1700000})
	e.a.DB.Model(&models.BudgetLimit{}).Where("budget_id = ?", b.ID).Count(&n)
	eq(t, "limit rows after upsert", n, 2)
	eq(t, "nov monthlyLimit", one(t, e.list(t, "2026-11")).MonthlyLimit, 1700000)
	eq(t, "oct monthlyLimit", one(t, e.list(t, "2026-10")).MonthlyLimit, 1000000)
}

func TestBudgets_ToggleRolloverResetsCarry(t *testing.T) {
	e := newEnv(t)
	testutil.SetNow(t, wib(2026, 9, 15, 12, 0))
	food := e.food(t)
	b := e.create(t, food.ID, 1000000, true)
	e.spend(t, food, 800000, wib(2026, 9, 12, 12, 0))
	testutil.SetNow(t, wib(2026, 10, 5, 12, 0))
	oct := one(t, e.list(t, "2026-10"))
	eq(t, "carryover", oct.Carryover, 200000)
	eq(t, "available", oct.Available, 1200000)
	v := e.patch(t, b.ID, map[string]any{"rolloverEnabled": false}).Budget
	if v.RolloverEnabled {
		t.Error("rolloverEnabled should be false")
	}
	eq(t, "carryover", v.Carryover, 0)
	eq(t, "available", v.Available, 1000000)
	if e.reload(t, b.ID).RolloverSince != nil {
		t.Error("RolloverSince should be nil")
	}
	testutil.SetNow(t, wib(2026, 10, 6, 12, 0))
	e.spend(t, food, 300000, wib(2026, 10, 3, 9, 0))
	v = e.patch(t, b.ID, map[string]any{"rolloverEnabled": true}).Budget
	nums(t, v, 0, 1000000, 300000, 700000)
	eq(t, "percentUsed", v.PercentUsed, 30)
	rs := e.reload(t, b.ID).RolloverSince
	if rs == nil || !rs.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("RolloverSince = %v, want 2026-10-01", rs)
	}
	testutil.SetNow(t, wib(2026, 11, 2, 12, 0))
	nov := one(t, e.list(t, "2026-11"))
	eq(t, "nov carryover", nov.Carryover, 700000)
	eq(t, "nov available", nov.Available, 1700000)
	eq(t, "oct carryover", one(t, e.list(t, "2026-10")).Carryover, 0)
}

func TestBudgets_PatchEmptyBodyAndInactive(t *testing.T) {
	e := newEnv(t)
	testutil.SetNow(t, wib(2026, 10, 15, 12, 0))
	b := e.create(t, e.food(t).ID, 1000000, false)
	v := e.patch(t, b.ID, map[string]any{}).Budget
	nums(t, v, 0, 1000000, 0, 1000000)
	path := "/api/v1/budgets/" + b.ID.String()
	r := e.a.Do(t, "PATCH", path, map[string]any{"monthlyLimit": 0}, e.cookie)
	eq(t, "status", int64(r.Status), 400)
	eqS(t, "field", r.Field("monthlyLimit"), "Budget must be greater than 0.")
	e.a.DB.Model(&models.Budget{}).Where("id = ?", b.ID).Update("status", models.StatusInactive)
	r = e.a.Do(t, "PATCH", path, map[string]any{"monthlyLimit": 500000}, e.cookie)
	eq(t, "status", int64(r.Status), 400)
	eqS(t, "code", r.ErrCode(), "BUDGET_INACTIVE")
}

func TestBudgets_ActivateRestartsCarry(t *testing.T) {
	e := newEnv(t)
	testutil.SetNow(t, wib(2026, 9, 15, 12, 0))
	food := e.food(t)
	b := e.create(t, food.ID, 1000000, true)
	e.spend(t, food, 800000, wib(2026, 9, 12, 12, 0))
	e.a.DB.Model(&models.Category{}).Where("id = ?", food.ID).Update("status", models.StatusArchived)
	e.a.DB.Model(&models.Budget{}).Where("id = ?", b.ID).Update("status", models.StatusInactive)
	testutil.SetNow(t, wib(2026, 10, 5, 12, 0))
	l := e.list(t, "2026-10")
	if len(l.Budgets) != 0 || len(l.Inactive) != 1 {
		t.Fatalf("budgets=%d inactive=%d", len(l.Budgets), len(l.Inactive))
	}
	eqS(t, "inactive name", l.Inactive[0].Category.Name, "Food & Drinks")
	if !l.Inactive[0].Category.Archived {
		t.Error("category should be archived")
	}
	path := "/api/v1/budgets/" + b.ID.String() + "/activate"
	r := e.a.Do(t, "POST", path, nil, e.cookie)
	eq(t, "status", int64(r.Status), 400)
	eqS(t, "code", r.ErrCode(), "CATEGORY_ARCHIVED")
	eqS(t, "message", r.Body["error"].(map[string]any)["message"].(string), "Unarchive the category first")
	e.a.DB.Model(&models.Category{}).Where("id = ?", food.ID).Update("status", models.StatusActive)
	r = e.a.Do(t, "POST", path, nil, e.cookie)
	eq(t, "status", int64(r.Status), 200)
	var out budgetResp
	r.Decode(t, &out)
	nums(t, out.Budget, 0, 1000000, 0, 1000000)
	got := e.reload(t, b.ID)
	eqS(t, "status", got.Status, models.StatusActive)
	if got.RolloverSince == nil || !got.RolloverSince.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("RolloverSince = %v", got.RolloverSince)
	}
	testutil.SetNow(t, wib(2026, 11, 3, 12, 0))
	nov := one(t, e.list(t, "2026-11"))
	eq(t, "carryover", nov.Carryover, 1000000)
	eq(t, "available", nov.Available, 2000000)
}

func TestBudgets_ActivateAlreadyActiveIsNoop(t *testing.T) {
	e := newEnv(t)
	testutil.SetNow(t, wib(2026, 10, 15, 12, 0))
	b := e.create(t, e.food(t).ID, 1000000, true)
	before := e.reload(t, b.ID).RolloverSince
	testutil.SetNow(t, wib(2026, 11, 15, 12, 0))
	r := e.a.Do(t, "POST", "/api/v1/budgets/"+b.ID.String()+"/activate", nil, e.cookie)
	eq(t, "status", int64(r.Status), 200)
	after := e.reload(t, b.ID).RolloverSince
	if before == nil || after == nil || !before.Equal(*after) {
		t.Errorf("RolloverSince changed: %v -> %v", before, after)
	}
}

func TestBudgets_DuplicateBudget409(t *testing.T) {
	e := newEnv(t)
	testutil.SetNow(t, wib(2026, 10, 15, 12, 0))
	food := e.food(t)
	b := e.create(t, food.ID, 1000000, false)
	body := map[string]any{"categoryId": food.ID.String(), "monthlyLimit": 500000}
	r := e.a.Do(t, "POST", "/api/v1/budgets", body, e.cookie)
	eq(t, "status", int64(r.Status), 409)
	eqS(t, "code", r.ErrCode(), "BUDGET_EXISTS")
	e.a.DB.Model(&models.Budget{}).Where("id = ?", b.ID).Update("status", models.StatusInactive)
	r = e.a.Do(t, "POST", "/api/v1/budgets", body, e.cookie)
	eq(t, "status", int64(r.Status), 409)
	eqS(t, "code", r.ErrCode(), "BUDGET_EXISTS")
}

func TestBudgets_CategoryValidation(t *testing.T) {
	e := newEnv(t)
	testutil.SetNow(t, wib(2026, 10, 15, 12, 0))
	salary := e.cat(t, e.user.ID, "Salary", models.TypeIncome)
	transport := e.cat(t, e.user.ID, "Transport", models.TypeExpense)
	e.a.DB.Model(&models.Category{}).Where("id = ?", transport.ID).Update("status", models.StatusArchived)
	for _, id := range []string{salary.ID.String(), transport.ID.String(), uuid.NewString(), "abc", ""} {
		r := e.a.Do(t, "POST", "/api/v1/budgets", map[string]any{"categoryId": id, "monthlyLimit": 100000}, e.cookie)
		eq(t, "status "+id, int64(r.Status), 400)
		eqS(t, "field "+id, r.Field("categoryId"), "Choose an active expense category")
	}
}

func TestBudgets_OtherUsersCategoryRejected(t *testing.T) {
	e := newEnv(t)
	testutil.SetNow(t, wib(2026, 10, 15, 12, 0))
	b, _ := e.a.NewUser(t, "b@example.com")
	bFood := e.cat(t, b.ID, "Food & Drinks", models.TypeExpense)
	r := e.a.Do(t, "POST", "/api/v1/budgets", map[string]any{"categoryId": bFood.ID.String(), "monthlyLimit": 100000}, e.cookie)
	eq(t, "status", int64(r.Status), 400)
	eqS(t, "field", r.Field("categoryId"), "Choose an active expense category")
}

func TestBudgets_US73_StatusThresholds(t *testing.T) {
	e := newEnv(t)
	testutil.SetNow(t, wib(2026, 10, 15, 12, 0))
	food := e.food(t)
	e.create(t, food.ID, 1000000, false)
	steps := []struct {
		add, pct, remaining int64
		status              string
	}{{790000, 79, 210000, "green"}, {10000, 80, 200000, "amber"}, {200000, 100, 0, "red"}, {50000, 105, -50000, "red"}}
	for _, s := range steps {
		e.spend(t, food, s.add, wib(2026, 10, 10, 12, 0))
		v := one(t, e.list(t, "2026-10"))
		eq(t, "percentUsed", v.PercentUsed, s.pct)
		eq(t, "remaining", v.Remaining, s.remaining)
		eqS(t, "status", v.Status, s.status)
	}
}

func TestBudgets_BackdatedExpenseRecalculatesLaterMonths(t *testing.T) {
	e := newEnv(t)
	testutil.SetNow(t, wib(2026, 9, 15, 12, 0))
	food := e.food(t)
	e.create(t, food.ID, 1000000, true)
	testutil.SetNow(t, wib(2026, 10, 3, 12, 0))
	oct := one(t, e.list(t, "2026-10"))
	eq(t, "carryover", oct.Carryover, 1000000)
	eq(t, "available", oct.Available, 2000000)
	e.spend(t, food, 400000, wib(2026, 9, 20, 12, 0))
	oct = one(t, e.list(t, "2026-10"))
	eq(t, "carryover", oct.Carryover, 600000)
	eq(t, "available", oct.Available, 1600000)
}

func TestBudgets_DeleteRemovesLimits(t *testing.T) {
	e := newEnv(t)
	testutil.SetNow(t, wib(2026, 10, 15, 12, 0))
	food := e.food(t)
	b := e.create(t, food.ID, 1000000, false)
	testutil.SetNow(t, wib(2026, 11, 2, 12, 0))
	e.patch(t, b.ID, map[string]any{"monthlyLimit": 1200000})
	path := "/api/v1/budgets/" + b.ID.String()
	eq(t, "status", int64(e.a.Do(t, "DELETE", path, nil, e.cookie).Status), 204)
	var n int64
	e.a.DB.Model(&models.Budget{}).Where("id = ?", b.ID).Count(&n)
	eq(t, "budgets", n, 0)
	e.a.DB.Model(&models.BudgetLimit{}).Where("budget_id = ?", b.ID).Count(&n)
	eq(t, "limits", n, 0)
	if len(e.list(t, "").Budgets) != 0 {
		t.Error("budgets should be empty")
	}
	eq(t, "second delete", int64(e.a.Do(t, "DELETE", path, nil, e.cookie).Status), 404)
	e.a.DB.Model(&models.Category{}).Where("id = ?", food.ID).Count(&n)
	eq(t, "category", n, 1)
}

func TestBudgets_OtherUserGets404(t *testing.T) {
	e := newEnv(t)
	testutil.SetNow(t, wib(2026, 10, 15, 12, 0))
	b := e.create(t, e.food(t).ID, 1000000, false)
	_, bCookie := e.a.NewUser(t, "b@example.com")
	path := "/api/v1/budgets/" + b.ID.String()
	eq(t, "patch", int64(e.a.Do(t, "PATCH", path, map[string]any{"monthlyLimit": 5}, bCookie).Status), 404)
	eq(t, "activate", int64(e.a.Do(t, "POST", path+"/activate", nil, bCookie).Status), 404)
	eq(t, "delete", int64(e.a.Do(t, "DELETE", path, nil, bCookie).Status), 404)
	r := e.a.Do(t, "GET", "/api/v1/budgets", nil, bCookie)
	if bs, ok := r.Body["budgets"].([]any); !ok || len(bs) != 0 {
		t.Errorf("B budgets should be []: %s", r.Raw)
	}
	eq(t, "A budget still there", int64(len(e.list(t, "").Budgets)), 1)
	eq(t, "not-a-uuid", int64(e.a.Do(t, "PATCH", "/api/v1/budgets/not-a-uuid", map[string]any{}, e.cookie).Status), 404)
}

func TestBudgets_ListOrderAndInvalidMonth(t *testing.T) {
	e := newEnv(t)
	testutil.SetNow(t, wib(2026, 10, 15, 12, 0))
	e.create(t, e.cat(t, e.user.ID, "Transport", models.TypeExpense).ID, 100000, false)
	e.create(t, e.food(t).ID, 100000, false)
	l := e.list(t, "")
	if len(l.Budgets) != 2 || l.Budgets[0].Category.Name != "Food & Drinks" || l.Budgets[1].Category.Name != "Transport" {
		t.Fatalf("unexpected order: %+v", l.Budgets)
	}
	for _, m := range []string{"2026-13", "abc"} {
		r := e.a.Do(t, "GET", "/api/v1/budgets?month="+m, nil, e.cookie)
		eq(t, "status", int64(r.Status), 400)
		eqS(t, "field", r.Field("month"), "Enter a valid month (YYYY-MM)")
	}
}

func TestBudgets_RequiresAuth(t *testing.T) {
	a := testutil.NewApp(t)
	eq(t, "status", int64(a.Do(t, "GET", "/api/v1/budgets", nil, "").Status), 401)
}
