package recurring_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"beprod/internal/jakarta"
	"beprod/internal/models"
	"beprod/internal/recurring"
	"beprod/internal/testutil"
)

func ruleWIB(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, jakarta.Loc)
}

type ruleGetResp struct {
	Due   []recurring.OccurrenceDTO `json:"due"`
	Rules []recurring.RuleDTO       `json:"rules"`
}

type ruleEnv struct {
	a                          *testutil.TestApp
	user                       models.User
	cookie                     string
	bca, cash                  models.Account
	housing, salary, transport models.Category
}

func newRuleEnv(t *testing.T) *ruleEnv {
	t.Helper()
	a := testutil.NewApp(t)
	testutil.SetNow(t, ruleWIB(2026, 10, 20, 12, 0))
	u, cookie := a.NewUser(t, "a@example.com")
	e := &ruleEnv{a: a, user: u, cookie: cookie}
	open := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e.bca = models.Account{UserID: u.ID, Name: "BCA", Type: models.AccountBank, OpeningBalance: 1000000, OpeningDate: open, Status: models.StatusActive}
	e.cash = models.Account{UserID: u.ID, Name: "Cash", Type: models.AccountCash, OpeningDate: open, Status: models.StatusActive}
	if err := a.DB.Create(&e.bca).Error; err != nil {
		t.Fatal(err)
	}
	if err := a.DB.Create(&e.cash).Error; err != nil {
		t.Fatal(err)
	}
	cat := func(name, typ string) models.Category {
		var c models.Category
		if err := a.DB.Where("user_id = ? AND name = ? AND type = ?", u.ID, name, typ).First(&c).Error; err != nil {
			t.Fatal(err)
		}
		return c
	}
	e.housing, e.salary, e.transport = cat("Housing", models.TypeExpense), cat("Salary", models.TypeIncome), cat("Transport", models.TypeExpense)
	return e
}

// do calls the API as user A; sessions slide only 30 days and tests jump the frozen clock, so log in fresh each time.
func (e *ruleEnv) do(t *testing.T, method, path string, body any) testutil.Response {
	t.Helper()
	e.cookie = e.a.LoginAs(t, e.user.ID)
	return e.a.Do(t, method, path, body, e.cookie)
}

func (e *ruleEnv) body(over map[string]any) map[string]any {
	b := map[string]any{"type": "expense", "amount": 3000000, "accountId": e.bca.ID.String(), "categoryId": e.housing.ID.String(),
		"note": "Rent", "tags": []string{}, "frequency": "monthly", "startDate": "2026-11-01"}
	for k, v := range over {
		b[k] = v
	}
	return b
}

func (e *ruleEnv) create(t *testing.T, over map[string]any) recurring.RuleDTO {
	t.Helper()
	r := e.do(t, "POST", "/api/v1/recurring/rules", e.body(over))
	if r.Status != 201 {
		t.Fatalf("create rule: status %d body %s", r.Status, r.Raw)
	}
	var out struct {
		Rule recurring.RuleDTO `json:"rule"`
	}
	r.Decode(t, &out)
	return out.Rule
}

func (e *ruleEnv) get(t *testing.T) ruleGetResp {
	t.Helper()
	r := e.do(t, "GET", "/api/v1/recurring", nil)
	if r.Status != 200 {
		t.Fatalf("get recurring: status %d body %s", r.Status, r.Raw)
	}
	var out ruleGetResp
	r.Decode(t, &out)
	return out
}

func (e *ruleEnv) occCount(t *testing.T, ruleID uuid.UUID) int64 {
	t.Helper()
	var n int64
	if err := e.a.DB.Model(&models.RecurringOccurrence{}).Where("rule_id = ?", ruleID).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *ruleEnv) gen(t *testing.T) {
	t.Helper()
	if err := recurring.GenerateAll(e.a.DB); err != nil {
		t.Fatal(err)
	}
}

func (e *ruleEnv) action(t *testing.T, method string, id uuid.UUID, suffix string, body any) testutil.Response {
	t.Helper()
	return e.do(t, method, "/api/v1/recurring/rules/"+id.String()+suffix, body)
}

func ruleDecode(t *testing.T, r testutil.Response) recurring.RuleDTO {
	t.Helper()
	var out struct {
		Rule recurring.RuleDTO `json:"rule"`
	}
	r.Decode(t, &out)
	return out.Rule
}

func ruleWantDue(t *testing.T, got ruleGetResp, want ...string) {
	t.Helper()
	if len(got.Due) != len(want) {
		t.Fatalf("due = %d items, want %v", len(got.Due), want)
	}
	for i, w := range want {
		if got.Due[i].DueDate != w {
			t.Fatalf("due[%d] = %s, want %s", i, got.Due[i].DueDate, w)
		}
	}
}

func ruleEq(t *testing.T, what string, got, want any) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

func ruleFromDB(t *testing.T, e *ruleEnv, id uuid.UUID) models.RecurringRule {
	t.Helper()
	var r models.RecurringRule
	if err := e.a.DB.First(&r, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRule_US81_AC1_NoOccurrenceBeforeStart(t *testing.T) {
	e := newRuleEnv(t)
	rule := e.create(t, nil)
	ruleEq(t, "status", rule.Status, "active")
	ruleEq(t, "nextDueDate", rule.NextDueDate, "2026-11-01")
	g := e.get(t)
	ruleWantDue(t, g)
	ruleEq(t, "rules", len(g.Rules), 1)
	testutil.SetNow(t, ruleWIB(2026, 10, 31, 23, 59))
	e.gen(t)
	ruleEq(t, "occCount", e.occCount(t, rule.ID), int64(0))
	testutil.SetNow(t, ruleWIB(2026, 11, 1, 0, 6))
	e.gen(t)
	g = e.get(t)
	ruleWantDue(t, g, "2026-11-01")
	o := g.Due[0]
	ruleEq(t, "status", o.Status, "pending")
	ruleEq(t, "amount", o.Amount, int64(3000000))
	ruleEq(t, "type", o.Type, "expense")
	ruleEq(t, "category", o.Category.Name, "Housing")
	ruleEq(t, "account", o.Account.Name, "BCA")
	if o.TransactionID != nil {
		t.Fatal("transactionId should be nil")
	}
	ruleEq(t, "nextDueDate", g.Rules[0].NextDueDate, "2026-12-01")
}

func TestRule_US81_AC2_Day31Clamp(t *testing.T) {
	e := newRuleEnv(t)
	e.create(t, map[string]any{"startDate": "2027-01-31"})
	step := func(now time.Time, want ...string) {
		t.Helper()
		testutil.SetNow(t, now)
		e.gen(t)
		ruleWantDue(t, e.get(t), want...)
	}
	step(ruleWIB(2027, 1, 31, 0, 6), "2027-01-31")
	step(ruleWIB(2027, 2, 27, 12, 0), "2027-01-31")
	step(ruleWIB(2027, 2, 28, 0, 6), "2027-01-31", "2027-02-28")
	step(ruleWIB(2027, 3, 31, 0, 6), "2027-01-31", "2027-02-28", "2027-03-31")
	step(ruleWIB(2027, 4, 30, 0, 6), "2027-01-31", "2027-02-28", "2027-03-31", "2027-04-30")
	ruleEq(t, "nextDueDate", e.get(t).Rules[0].NextDueDate, "2027-05-31")
}

func TestRule_US82_AC4_ThreeMissedMonths(t *testing.T) {
	e := newRuleEnv(t)
	e.create(t, nil)
	testutil.SetNow(t, ruleWIB(2027, 1, 15, 12, 0))
	e.gen(t)
	g := e.get(t)
	ruleWantDue(t, g, "2026-11-01", "2026-12-01", "2027-01-01")
	ruleEq(t, "nextDueDate", g.Rules[0].NextDueDate, "2027-02-01")
	got, total, err := recurring.Pending(e.a.DB, e.user.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	ruleEq(t, "limited len", len(got), 2)
	ruleEq(t, "limited total", total, int64(3))
	ruleEq(t, "limited[0]", got[0].DueDate, "2026-11-01")
	ruleEq(t, "limited[1]", got[1].DueDate, "2026-12-01")
	got, total, err = recurring.Pending(e.a.DB, e.user.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	ruleEq(t, "all len", len(got), 3)
	ruleEq(t, "all total", total, int64(3))
}

func TestRule_GenerateIsIdempotent(t *testing.T) {
	e := newRuleEnv(t)
	rule := e.create(t, nil)
	testutil.SetNow(t, ruleWIB(2027, 1, 15, 12, 0))
	e.gen(t)
	e.gen(t)
	ruleEq(t, "occCount", e.occCount(t, rule.ID), int64(3))
	if err := e.a.DB.Exec("UPDATE recurring_rules SET next_due_date = '2026-11-01' WHERE id = ?", rule.ID).Error; err != nil {
		t.Fatal(err)
	}
	e.gen(t)
	ruleEq(t, "occCount after reset", e.occCount(t, rule.ID), int64(3))
	ruleEq(t, "next_due_date", jakarta.FormatDate(ruleFromDB(t, e, rule.ID).NextDueDate), "2027-02-01")
}

func TestRule_StartingTodayGetsOccurrenceAtCreate(t *testing.T) {
	e := newRuleEnv(t)
	rule := e.create(t, map[string]any{"startDate": "2026-10-20"})
	ruleEq(t, "nextDueDate", rule.NextDueDate, "2026-11-20")
	ruleEq(t, "status", rule.Status, "active")
	ruleWantDue(t, e.get(t), "2026-10-20")
}

func TestRule_EndDateEndsAfterLastOccurrence(t *testing.T) {
	e := newRuleEnv(t)
	rule := e.create(t, map[string]any{"endDate": "2027-01-01"})
	testutil.SetNow(t, ruleWIB(2026, 12, 5, 12, 0))
	e.gen(t)
	ruleEq(t, "occCount", e.occCount(t, rule.ID), int64(2))
	r := e.get(t).Rules[0]
	ruleEq(t, "status", r.Status, "active")
	ruleEq(t, "nextDueDate", r.NextDueDate, "2027-01-01")
	testutil.SetNow(t, ruleWIB(2027, 1, 2, 12, 0))
	e.gen(t)
	ruleEq(t, "occCount", e.occCount(t, rule.ID), int64(3))
	r = e.get(t).Rules[0]
	ruleEq(t, "status", r.Status, "ended")
	ruleEq(t, "nextDueDate", r.NextDueDate, "2027-02-01")
	testutil.SetNow(t, ruleWIB(2027, 3, 5, 12, 0))
	e.gen(t)
	ruleEq(t, "occCount", e.occCount(t, rule.ID), int64(3))
}

func TestRule_EndDateSameAsStartToday(t *testing.T) {
	e := newRuleEnv(t)
	rule := e.create(t, map[string]any{"frequency": "daily", "startDate": "2026-10-20", "endDate": "2026-10-20"})
	ruleEq(t, "status", rule.Status, "ended")
	ruleEq(t, "nextDueDate", rule.NextDueDate, "2026-10-21")
	ruleWantDue(t, e.get(t), "2026-10-20")
}

func TestRule_PauseStopsGeneration(t *testing.T) {
	e := newRuleEnv(t)
	rule := e.create(t, nil)
	r := e.action(t, "POST", rule.ID, "/pause", nil)
	ruleEq(t, "pause status", r.Status, 200)
	p := ruleDecode(t, r)
	ruleEq(t, "status", p.Status, "paused")
	ruleEq(t, "pausedReason", p.PausedReason, "user")
	testutil.SetNow(t, ruleWIB(2026, 12, 5, 12, 0))
	e.gen(t)
	ruleEq(t, "occCount", e.occCount(t, rule.ID), int64(0))
	r = e.action(t, "POST", rule.ID, "/pause", nil)
	ruleEq(t, "re-pause status", r.Status, 400)
	ruleEq(t, "code", r.ErrCode(), "RULE_NOT_ACTIVE")
	second := e.create(t, map[string]any{"startDate": "2027-01-01"})
	r = e.action(t, "POST", second.ID, "/resume", nil)
	ruleEq(t, "resume active status", r.Status, 400)
	ruleEq(t, "code", r.ErrCode(), "RULE_NOT_PAUSED")
}

func TestRule_ResumeDoesNotBackfill(t *testing.T) {
	e := newRuleEnv(t)
	rule := e.create(t, nil)
	ruleEq(t, "pause", e.action(t, "POST", rule.ID, "/pause", nil).Status, 200)
	testutil.SetNow(t, ruleWIB(2027, 1, 15, 12, 0))
	r := e.action(t, "POST", rule.ID, "/resume", nil)
	ruleEq(t, "resume status", r.Status, 200)
	p := ruleDecode(t, r)
	ruleEq(t, "status", p.Status, "active")
	ruleEq(t, "pausedReason", p.PausedReason, "")
	ruleEq(t, "nextDueDate", p.NextDueDate, "2027-02-01")
	ruleWantDue(t, e.get(t))
	ruleEq(t, "occCount", e.occCount(t, rule.ID), int64(0))
	testutil.SetNow(t, ruleWIB(2027, 2, 1, 0, 6))
	e.gen(t)
	ruleWantDue(t, e.get(t), "2027-02-01")
}

func TestRule_ResumeOnScheduleDateGeneratesNow(t *testing.T) {
	e := newRuleEnv(t)
	rule := e.create(t, nil)
	e.action(t, "POST", rule.ID, "/pause", nil)
	testutil.SetNow(t, ruleWIB(2026, 12, 1, 12, 0))
	r := e.action(t, "POST", rule.ID, "/resume", nil)
	ruleEq(t, "resume status", r.Status, 200)
	ruleEq(t, "nextDueDate", ruleDecode(t, r).NextDueDate, "2027-01-01")
	ruleWantDue(t, e.get(t), "2026-12-01")
}

func TestRule_ResumeWithArchivedAccount400(t *testing.T) {
	e := newRuleEnv(t)
	rule := e.create(t, nil)
	e.action(t, "POST", rule.ID, "/pause", nil)
	e.a.DB.Model(&models.Account{}).Where("id = ?", e.bca.ID).Update("status", models.StatusArchived)
	r := e.action(t, "POST", rule.ID, "/resume", nil)
	ruleEq(t, "status", r.Status, 400)
	ruleEq(t, "code", r.ErrCode(), "RULE_REFS_ARCHIVED")
	ruleEq(t, "message", r.Body["error"].(map[string]any)["message"], "Edit this rule to use an active account and category first")
	ruleEq(t, "db status", ruleFromDB(t, e, rule.ID).Status, "paused")
}

func TestRule_ResumeWithArchivedCategory400(t *testing.T) {
	e := newRuleEnv(t)
	rule := e.create(t, nil)
	e.action(t, "POST", rule.ID, "/pause", nil)
	e.a.DB.Model(&models.Category{}).Where("id = ?", e.housing.ID).Update("status", models.StatusArchived)
	r := e.action(t, "POST", rule.ID, "/resume", nil)
	ruleEq(t, "status", r.Status, 400)
	ruleEq(t, "code", r.ErrCode(), "RULE_REFS_ARCHIVED")
	ruleEq(t, "db status", ruleFromDB(t, e, rule.ID).Status, "paused")
}

func TestRule_US_FR87_PutAffectsOnlyFuture(t *testing.T) {
	e := newRuleEnv(t)
	rule := e.create(t, nil)
	testutil.SetNow(t, ruleWIB(2026, 11, 1, 0, 6))
	e.gen(t)
	r := e.action(t, "PUT", rule.ID, "", map[string]any{"amount": 3500000, "accountId": e.bca.ID.String(), "toAccountId": "",
		"categoryId": e.housing.ID.String(), "note": "Rent", "tags": []string{}, "endDate": nil, "type": "income", "frequency": "daily"})
	ruleEq(t, "put status", r.Status, 200)
	p := ruleDecode(t, r)
	ruleEq(t, "amount", p.Amount, int64(3500000))
	ruleEq(t, "type", p.Type, "expense")
	ruleEq(t, "frequency", p.Frequency, "monthly")
	var nov models.RecurringOccurrence
	e.a.DB.First(&nov, "rule_id = ?", rule.ID)
	ruleEq(t, "nov amount", nov.Amount, int64(3000000))
	testutil.SetNow(t, ruleWIB(2026, 12, 1, 0, 6))
	e.gen(t)
	g := e.get(t)
	ruleWantDue(t, g, "2026-11-01", "2026-12-01")
	ruleEq(t, "nov", g.Due[0].Amount, int64(3000000))
	ruleEq(t, "dec", g.Due[1].Amount, int64(3500000))
}

func TestRule_PutValidationAndEnd(t *testing.T) {
	e := newRuleEnv(t)
	rule := e.create(t, nil)
	testutil.SetNow(t, ruleWIB(2026, 12, 5, 12, 0))
	e.gen(t)
	put := func(over map[string]any) testutil.Response {
		b := map[string]any{"amount": 3000000, "accountId": e.bca.ID.String(), "categoryId": e.housing.ID.String(),
			"note": "Rent", "tags": []string{}, "endDate": nil}
		for k, v := range over {
			b[k] = v
		}
		return e.action(t, "PUT", rule.ID, "", b)
	}
	r := put(map[string]any{"amount": 0})
	ruleEq(t, "status", r.Status, 400)
	ruleEq(t, "amount", r.Field("amount"), "Amount must be greater than 0")
	r = put(map[string]any{"endDate": "2026-10-31"})
	ruleEq(t, "status", r.Status, 400)
	ruleEq(t, "endDate", r.Field("endDate"), "End date must be on or after the start date")
	r = put(map[string]any{"endDate": "2026-12-15"})
	ruleEq(t, "status", r.Status, 200)
	ruleEq(t, "rule status", ruleDecode(t, r).Status, "ended")
}

func TestRule_PutKeepsArchivedAccount(t *testing.T) {
	e := newRuleEnv(t)
	rule := e.create(t, nil)
	other := models.Account{UserID: e.user.ID, Name: "Old", Type: models.AccountBank, OpeningDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Status: models.StatusArchived}
	if err := e.a.DB.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	e.a.DB.Model(&models.Account{}).Where("id = ?", e.bca.ID).Update("status", models.StatusArchived)
	put := func(acc uuid.UUID) testutil.Response {
		return e.action(t, "PUT", rule.ID, "", map[string]any{"amount": 3000000, "accountId": acc.String(),
			"categoryId": e.housing.ID.String(), "note": "Rent", "tags": []string{}, "endDate": nil})
	}
	ruleEq(t, "same archived", put(e.bca.ID).Status, 200)
	r := put(other.ID)
	ruleEq(t, "other archived", r.Status, 400)
	ruleEq(t, "accountId", r.Field("accountId"), "This account is no longer available")
}

func TestRule_DeleteKeepsConfirmedHistory(t *testing.T) {
	e := newRuleEnv(t)
	rule := e.create(t, nil)
	testutil.SetNow(t, ruleWIB(2026, 11, 1, 0, 6))
	e.gen(t)
	occ := models.RecurringOccurrence{RuleID: rule.ID, UserID: e.user.ID, DueDate: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		Status: models.OccConfirmed, Type: models.TypeExpense, Amount: 3000000, AccountID: e.bca.ID, CategoryID: &e.housing.ID, Tags: pq.StringArray{}}
	if err := e.a.DB.Create(&occ).Error; err != nil {
		t.Fatal(err)
	}
	tx := models.Transaction{UserID: e.user.ID, Type: models.TypeExpense, Amount: 3000000, OccurredAt: ruleWIB(2026, 10, 1, 0, 0),
		AccountID: e.bca.ID, CategoryID: &e.housing.ID, Tags: pq.StringArray{}, Source: models.SourceRecurring, RecurringOccurrenceID: &occ.ID}
	if err := e.a.DB.Create(&tx).Error; err != nil {
		t.Fatal(err)
	}
	e.a.DB.Model(&occ).Update("transaction_id", tx.ID)
	ruleEq(t, "delete", e.action(t, "DELETE", rule.ID, "", nil).Status, 204)
	count := func(m any, where string, args ...any) int64 {
		var n int64
		e.a.DB.Model(m).Where(where, args...).Count(&n)
		return n
	}
	ruleEq(t, "pending", count(&models.RecurringOccurrence{}, "rule_id = ? AND status = ?", rule.ID, models.OccPending), int64(0))
	ruleEq(t, "confirmed", count(&models.RecurringOccurrence{}, "rule_id = ? AND status = ?", rule.ID, models.OccConfirmed), int64(1))
	ruleEq(t, "transactions", count(&models.Transaction{}, "user_id = ?", e.user.ID), int64(1))
	ruleEq(t, "rules", count(&models.RecurringRule{}, "id = ?", rule.ID), int64(0))
	g := e.get(t)
	ruleEq(t, "due", len(g.Due), 0)
	ruleEq(t, "rules list", len(g.Rules), 0)
	ruleEq(t, "delete again", e.action(t, "DELETE", rule.ID, "", nil).Status, 404)
}

func TestRule_CreateValidation(t *testing.T) {
	e := newRuleEnv(t)
	post := func(over map[string]any) testutil.Response {
		return e.do(t, "POST", "/api/v1/recurring/rules", e.body(over))
	}
	r := post(map[string]any{"startDate": "2026-10-19"})
	ruleEq(t, "past status", r.Status, 400)
	ruleEq(t, "past", r.Field("startDate"), "Start date can't be in the past")
	for _, s := range []string{"abc", ""} {
		r = post(map[string]any{"startDate": s})
		ruleEq(t, "invalid start status", r.Status, 400)
		ruleEq(t, "invalid start", r.Field("startDate"), "Enter a valid date")
	}
	r = post(map[string]any{"startDate": "2026-11-10", "endDate": "2026-11-09"})
	ruleEq(t, "end<start", r.Field("endDate"), "End date must be on or after the start date")
	r = post(map[string]any{"endDate": "xx"})
	ruleEq(t, "bad end", r.Field("endDate"), "Enter a valid date")
	r = post(map[string]any{"frequency": "hourly", "amount": 0})
	ruleEq(t, "freq", r.Field("frequency"), "Choose daily, weekly, monthly or yearly")
	ruleEq(t, "amount", r.Field("amount"), "Amount must be greater than 0")
	var n int64
	e.a.DB.Model(&models.RecurringRule{}).Count(&n)
	ruleEq(t, "rules stored", n, int64(0))
	ruleEq(t, "today ok", post(map[string]any{"startDate": "2026-10-20"}).Status, 201)
}

func TestRule_TransferValidationAndSnapshot(t *testing.T) {
	e := newRuleEnv(t)
	post := func(over map[string]any) testutil.Response {
		over["type"] = "transfer"
		return e.do(t, "POST", "/api/v1/recurring/rules", e.body(over))
	}
	r := post(map[string]any{"toAccountId": e.bca.ID.String()})
	ruleEq(t, "same", r.Field("toAccountId"), "Choose two different accounts.")
	r = post(map[string]any{})
	ruleEq(t, "missing", r.Field("toAccountId"), "Select a destination account")
	r = post(map[string]any{"toAccountId": e.cash.ID.String(), "note": "Move", "startDate": "2026-10-20"})
	ruleEq(t, "status", r.Status, 201)
	rule := ruleDecode(t, r)
	if rule.Category != nil {
		t.Fatal("category should be nil")
	}
	ruleEq(t, "toAccount", rule.ToAccount.Name, "Cash")
	g := e.get(t)
	ruleWantDue(t, g, "2026-10-20")
	d := g.Due[0]
	ruleEq(t, "type", d.Type, "transfer")
	ruleEq(t, "toAccount", d.ToAccount.Name, "Cash")
	ruleEq(t, "amount", d.Amount, int64(3000000))
	if d.Category != nil {
		t.Fatal("occurrence category should be nil")
	}
}

func TestRule_OccurrenceIsSnapshot(t *testing.T) {
	e := newRuleEnv(t)
	rule := e.create(t, map[string]any{"note": "Rent Nov", "tags": []string{"Home", "#rent"}, "startDate": "2026-10-20"})
	if len(rule.Tags) != 2 || rule.Tags[0] != "home" || rule.Tags[1] != "rent" {
		t.Fatalf("rule tags = %v", rule.Tags)
	}
	o := e.get(t).Due[0]
	ruleEq(t, "note", o.Note, "Rent Nov")
	if len(o.Tags) != 2 || o.Tags[0] != "home" || o.Tags[1] != "rent" {
		t.Fatalf("occ tags = %v", o.Tags)
	}
	ruleEq(t, "amount", o.Amount, rule.Amount)
	ruleEq(t, "account", o.Account.ID, rule.Account.ID)
	ruleEq(t, "category", o.Category.ID, rule.Category.ID)
}

func TestRule_PendingDoesNotChangeBalance(t *testing.T) {
	e := newRuleEnv(t)
	e.create(t, map[string]any{"amount": 300000, "startDate": "2026-10-20"})
	r := e.do(t, "GET", "/api/v1/accounts/"+e.bca.ID.String(), nil)
	var out struct {
		Account struct {
			Balance int64 `json:"balance"`
		} `json:"account"`
	}
	r.Decode(t, &out)
	ruleEq(t, "balance", out.Account.Balance, int64(1000000))
	var n int64
	e.a.DB.Model(&models.Transaction{}).Count(&n)
	ruleEq(t, "transactions", n, int64(0))
}

func TestRule_OtherUserGets404(t *testing.T) {
	e := newRuleEnv(t)
	rule := e.create(t, nil)
	_, bCookie := e.a.NewUser(t, "b@example.com")
	base := "/api/v1/recurring/rules/" + rule.ID.String()
	put := map[string]any{"amount": 1, "accountId": e.bca.ID.String(), "categoryId": e.housing.ID.String(), "tags": []string{}}
	ruleEq(t, "put", e.a.Do(t, "PUT", base, put, bCookie).Status, 404)
	ruleEq(t, "pause", e.a.Do(t, "POST", base+"/pause", nil, bCookie).Status, 404)
	ruleEq(t, "resume", e.a.Do(t, "POST", base+"/resume", nil, bCookie).Status, 404)
	ruleEq(t, "delete", e.a.Do(t, "DELETE", base, nil, bCookie).Status, 404)
	r := e.a.Do(t, "GET", "/api/v1/recurring", nil, bCookie)
	var g ruleGetResp
	r.Decode(t, &g)
	ruleEq(t, "b due", len(g.Due), 0)
	ruleEq(t, "b rules", len(g.Rules), 0)
	r = e.a.Do(t, "POST", "/api/v1/recurring/rules", e.body(nil), bCookie)
	ruleEq(t, "b create", r.Status, 400)
	ruleEq(t, "accountId", r.Field("accountId"), "This account is no longer available")
	ruleEq(t, "a rules", len(e.get(t).Rules), 1)
	ruleEq(t, "bad uuid", e.do(t, "PUT", "/api/v1/recurring/rules/not-a-uuid", put).Status, 404)
}

func TestRule_RequiresAuth(t *testing.T) {
	e := newRuleEnv(t)
	ruleEq(t, "status", e.a.Do(t, "GET", "/api/v1/recurring", nil, "").Status, 401)
}

func TestRunScheduler_RunsOnceAndStopsOnCancel(t *testing.T) {
	e := newRuleEnv(t)
	day := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	rule := models.RecurringRule{UserID: e.user.ID, Type: models.TypeExpense, Amount: 100000, AccountID: e.bca.ID, CategoryID: &e.housing.ID,
		Tags: pq.StringArray{}, Frequency: models.FreqMonthly, StartDate: day, NextDueDate: day, Status: models.StatusActive}
	if err := e.a.DB.Create(&rule).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { recurring.RunScheduler(ctx, e.a.DB); close(done) }()
	for i := 0; i < 40 && e.occCount(t, rule.ID) < 1; i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if e.occCount(t, rule.ID) < 1 {
		t.Fatal("scheduler did not generate on start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not stop")
	}
}
