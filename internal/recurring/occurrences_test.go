package recurring_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"beprod/internal/jakarta"
	"beprod/internal/models"
	"beprod/internal/testutil"
	"beprod/internal/transactions"
)

func occWIB(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, jakarta.Loc)
}
func occDay(y int, mo time.Month, d int) time.Time { return time.Date(y, mo, d, 0, 0, 0, 0, time.UTC) }

type occEnv struct {
	a                  *testutil.TestApp
	user               models.User
	cookie             string
	bca, cash          models.Account
	housing, transport models.Category
	rule               models.RecurringRule
	occ                models.RecurringOccurrence
}

func newOccEnv(t *testing.T) *occEnv {
	t.Helper()
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "a@example.com")
	e := &occEnv{a: a, user: u, cookie: cookie}
	open := occDay(2026, 1, 1)
	e.bca = models.Account{UserID: u.ID, Name: "BCA", Type: models.AccountBank, OpeningBalance: 10000000, OpeningDate: open, Status: models.StatusActive}
	e.cash = models.Account{UserID: u.ID, Name: "Cash", Type: models.AccountCash, OpeningDate: open, Status: models.StatusActive}
	for _, acc := range []*models.Account{&e.bca, &e.cash} {
		if err := a.DB.Create(acc).Error; err != nil {
			t.Fatal(err)
		}
	}
	cat := func(name string) models.Category {
		var c models.Category
		if err := a.DB.Where("user_id = ? AND name = ? AND type = ?", u.ID, name, models.TypeExpense).First(&c).Error; err != nil {
			t.Fatal(err)
		}
		return c
	}
	e.housing, e.transport = cat("Housing"), cat("Transport")
	e.rule = models.RecurringRule{UserID: u.ID, Type: models.TypeExpense, Amount: 3000000, AccountID: e.bca.ID, CategoryID: &e.housing.ID,
		Note: "Rent", Tags: pq.StringArray{"home"}, Frequency: models.FreqMonthly, StartDate: occDay(2026, 11, 1),
		NextDueDate: occDay(2026, 12, 1), Status: models.StatusActive}
	if err := a.DB.Create(&e.rule).Error; err != nil {
		t.Fatal(err)
	}
	e.occ = models.RecurringOccurrence{RuleID: e.rule.ID, UserID: u.ID, DueDate: occDay(2026, 11, 1), Status: models.OccPending,
		Type: models.TypeExpense, Amount: 3000000, AccountID: e.bca.ID, CategoryID: &e.housing.ID, Note: "Rent", Tags: pq.StringArray{"home"}}
	if err := a.DB.Create(&e.occ).Error; err != nil {
		t.Fatal(err)
	}
	testutil.SetNow(t, occWIB(2026, 11, 5, 12, 0))
	return e
}

func (e *occEnv) confirm(t *testing.T, body any) testutil.Response {
	t.Helper()
	return e.a.Do(t, "POST", fmt.Sprintf("/api/v1/recurring/occurrences/%s/confirm", e.occ.ID), body, e.cookie)
}

func (e *occEnv) skip(t *testing.T) testutil.Response {
	t.Helper()
	return e.a.Do(t, "POST", fmt.Sprintf("/api/v1/recurring/occurrences/%s/skip", e.occ.ID), nil, e.cookie)
}

func (e *occEnv) balance(t *testing.T, accountID uuid.UUID) int64 {
	t.Helper()
	r := e.a.Do(t, "GET", "/api/v1/accounts/"+accountID.String(), nil, e.cookie)
	var out struct {
		Account struct {
			Balance int64 `json:"balance"`
		} `json:"account"`
	}
	r.Decode(t, &out)
	return out.Account.Balance
}

func (e *occEnv) reloadOcc(t *testing.T) models.RecurringOccurrence {
	t.Helper()
	var o models.RecurringOccurrence
	if err := e.a.DB.First(&o, "id = ?", e.occ.ID).Error; err != nil {
		t.Fatal(err)
	}
	return o
}

func (e *occEnv) txCount(t *testing.T) int64 {
	t.Helper()
	var n int64
	e.a.DB.Model(&models.Transaction{}).Where("user_id = ?", e.user.ID).Count(&n)
	return n
}

func (e *occEnv) dueLen(t *testing.T) int {
	t.Helper()
	var out ruleGetResp
	e.a.Do(t, "GET", "/api/v1/recurring", nil, e.cookie).Decode(t, &out)
	return len(out.Due)
}

func occDecodeTx(t *testing.T, r testutil.Response) transactions.DTO {
	t.Helper()
	var out struct {
		Transaction transactions.DTO `json:"transaction"`
	}
	r.Decode(t, &out)
	return out.Transaction
}

func occWant(t *testing.T, r testutil.Response, status int) {
	t.Helper()
	if r.Status != status {
		t.Fatalf("status %d, want %d: %s", r.Status, status, r.Raw)
	}
}

func occWantErr(t *testing.T, r testutil.Response, status int, code string) {
	t.Helper()
	occWant(t, r, status)
	if r.ErrCode() != code {
		t.Fatalf("code %q, want %q", r.ErrCode(), code)
	}
}

func occWantStatus(t *testing.T, e *occEnv, want string) {
	t.Helper()
	if s := e.reloadOcc(t).Status; s != want {
		t.Fatalf("occurrence status %q, want %q", s, want)
	}
}

func TestConfirm_US82_AC1_CreatesRecurringTransaction(t *testing.T) {
	e := newOccEnv(t)
	r := e.confirm(t, nil)
	occWant(t, r, 201)
	tx := occDecodeTx(t, r)
	if tx.Type != "expense" || tx.Amount != 3000000 || tx.Source != "recurring" || tx.Date != "2026-11-01" || tx.Time != "00:00" ||
		tx.OccurredAt != "2026-11-01T00:00:00+07:00" || tx.Note != "Rent" || len(tx.Tags) != 1 || tx.Tags[0] != "home" ||
		tx.Category == nil || tx.Category.Name != "Housing" || tx.Account.Name != "BCA" {
		t.Fatalf("unexpected dto %+v", tx)
	}
	var m models.Transaction
	if err := e.a.DB.First(&m, "id = ?", tx.ID).Error; err != nil {
		t.Fatal(err)
	}
	if m.RecurringOccurrenceID == nil || *m.RecurringOccurrenceID != e.occ.ID {
		t.Fatalf("recurring_occurrence_id = %v", m.RecurringOccurrenceID)
	}
	o := e.reloadOcc(t)
	if o.Status != models.OccConfirmed || o.TransactionID == nil || *o.TransactionID != tx.ID || o.Amount != 3000000 {
		t.Fatalf("occurrence %+v", o)
	}
	if b := e.balance(t, e.bca.ID); b != 7000000 {
		t.Fatalf("balance %d", b)
	}
	if n := e.dueLen(t); n != 0 {
		t.Fatalf("due %d", n)
	}
}

func TestConfirm_US82_AC2_EditAmountKeepsRule(t *testing.T) {
	e := newOccEnv(t)
	r := e.confirm(t, map[string]any{"amount": 3100000})
	occWant(t, r, 201)
	tx := occDecodeTx(t, r)
	if tx.Amount != 3100000 || tx.Date != "2026-11-01" {
		t.Fatalf("dto %+v", tx)
	}
	if b := e.balance(t, e.bca.ID); b != 6900000 {
		t.Fatalf("balance %d", b)
	}
	var rule models.RecurringRule
	e.a.DB.First(&rule, "id = ?", e.rule.ID)
	if rule.Amount != 3000000 || e.reloadOcc(t).Amount != 3000000 {
		t.Fatal("rule/occurrence snapshot changed")
	}
}

func TestConfirm_OverrideFields(t *testing.T) {
	e := newOccEnv(t)
	r := e.confirm(t, map[string]any{"amount": 2500000, "date": "2026-11-03", "accountId": e.cash.ID.String(),
		"categoryId": e.transport.ID.String(), "note": "Rent November", "tags": []string{"Home", "#Rent"}})
	occWant(t, r, 201)
	tx := occDecodeTx(t, r)
	if tx.Amount != 2500000 || tx.Date != "2026-11-03" || tx.OccurredAt != "2026-11-03T00:00:00+07:00" || tx.Account.Name != "Cash" ||
		tx.Category == nil || tx.Category.Name != "Transport" || tx.Note != "Rent November" ||
		len(tx.Tags) != 2 || tx.Tags[0] != "home" || tx.Tags[1] != "rent" {
		t.Fatalf("dto %+v", tx)
	}
	var rule models.RecurringRule
	e.a.DB.First(&rule, "id = ?", e.rule.ID)
	if rule.AccountID != e.bca.ID || rule.CategoryID == nil || *rule.CategoryID != e.housing.ID || rule.Note != "Rent" {
		t.Fatalf("rule changed %+v", rule)
	}
}

func TestConfirm_EmptyNoteOverrideClearsNote(t *testing.T) {
	e := newOccEnv(t)
	r := e.confirm(t, map[string]any{"note": ""})
	occWant(t, r, 201)
	if n := occDecodeTx(t, r).Note; n != "" {
		t.Fatalf("note %q", n)
	}
}

func TestConfirm_DateInFutureRejected(t *testing.T) {
	e := newOccEnv(t)
	r := e.confirm(t, map[string]any{"date": "2026-11-06"})
	occWantErr(t, r, 400, "VALIDATION_ERROR")
	if f := r.Field("date"); f != "Date can't be in the future" {
		t.Fatalf("field %q", f)
	}
	occWantStatus(t, e, models.OccPending)
	if n := e.txCount(t); n != 0 {
		t.Fatalf("txCount %d", n)
	}
	r = e.confirm(t, map[string]any{"date": "abc"})
	occWantErr(t, r, 400, "VALIDATION_ERROR")
	if f := r.Field("date"); f != "Enter a valid date" {
		t.Fatalf("field %q", f)
	}
}

func TestConfirm_AmountValidation(t *testing.T) {
	e := newOccEnv(t)
	r := e.confirm(t, map[string]any{"amount": 0})
	occWantErr(t, r, 400, "VALIDATION_ERROR")
	if f := r.Field("amount"); f != "Amount must be greater than 0" {
		t.Fatalf("field %q", f)
	}
	occWantStatus(t, e, models.OccPending)
	occWantErr(t, e.confirm(t, "not an object"), 400, "INVALID_BODY")
}

func TestSkip_US82_AC3(t *testing.T) {
	e := newOccEnv(t)
	occWant(t, e.skip(t), 204)
	o := e.reloadOcc(t)
	if o.Status != models.OccSkipped || o.TransactionID != nil {
		t.Fatalf("occurrence %+v", o)
	}
	if n := e.txCount(t); n != 0 {
		t.Fatalf("txCount %d", n)
	}
	if b := e.balance(t, e.bca.ID); b != 10000000 {
		t.Fatalf("balance %d", b)
	}
	if n := e.dueLen(t); n != 0 {
		t.Fatalf("due %d", n)
	}
}

func TestConfirm_Twice409(t *testing.T) {
	e := newOccEnv(t)
	occWant(t, e.confirm(t, nil), 201)
	r := e.confirm(t, nil)
	occWantErr(t, r, 409, "OCCURRENCE_NOT_PENDING")
	if m, _ := r.Body["error"].(map[string]any)["message"].(string); m != "This item was already handled" {
		t.Fatalf("message %q", m)
	}
	if n := e.txCount(t); n != 1 {
		t.Fatalf("txCount %d", n)
	}
}

func TestSkip_AfterConfirm409(t *testing.T) {
	e := newOccEnv(t)
	occWant(t, e.confirm(t, nil), 201)
	occWantErr(t, e.skip(t), 409, "OCCURRENCE_NOT_PENDING")
	occWantStatus(t, e, models.OccConfirmed)
}

func TestConfirm_AfterSkip409AndSkipTwice409(t *testing.T) {
	e := newOccEnv(t)
	occWant(t, e.skip(t), 204)
	occWantErr(t, e.confirm(t, nil), 409, "OCCURRENCE_NOT_PENDING")
	occWantErr(t, e.skip(t), 409, "OCCURRENCE_NOT_PENDING")
	if n := e.txCount(t); n != 0 {
		t.Fatalf("txCount %d", n)
	}
}

func TestConfirm_ArchivedCategoryThenOverride(t *testing.T) {
	e := newOccEnv(t)
	e.a.DB.Model(&models.Category{}).Where("id = ?", e.housing.ID).Update("status", models.StatusArchived)
	r := e.confirm(t, nil)
	occWantErr(t, r, 400, "VALIDATION_ERROR")
	if f := r.Field("categoryId"); f != "This category is no longer available" {
		t.Fatalf("field %q", f)
	}
	occWantStatus(t, e, models.OccPending)
	if n := e.txCount(t); n != 0 {
		t.Fatalf("txCount %d", n)
	}
	r = e.confirm(t, map[string]any{"categoryId": e.transport.ID.String()})
	occWant(t, r, 201)
	if tx := occDecodeTx(t, r); tx.Category == nil || tx.Category.Name != "Transport" {
		t.Fatalf("dto %+v", tx)
	}
	occWantStatus(t, e, models.OccConfirmed)
	var rule models.RecurringRule
	e.a.DB.First(&rule, "id = ?", e.rule.ID)
	if rule.CategoryID == nil || *rule.CategoryID != e.housing.ID {
		t.Fatal("rule category changed")
	}
}

func TestConfirm_ArchivedAccountThenOverride(t *testing.T) {
	e := newOccEnv(t)
	e.a.DB.Model(&models.Account{}).Where("id = ?", e.bca.ID).Update("status", models.StatusArchived)
	r := e.confirm(t, nil)
	occWantErr(t, r, 400, "VALIDATION_ERROR")
	if f := r.Field("accountId"); f != "This account is no longer available" {
		t.Fatalf("field %q", f)
	}
	r = e.confirm(t, map[string]any{"accountId": e.cash.ID.String()})
	occWant(t, r, 201)
	if tx := occDecodeTx(t, r); tx.Account.Name != "Cash" {
		t.Fatalf("dto %+v", tx)
	}
}

func TestConfirm_DeletingTransactionDoesNotReopen(t *testing.T) {
	e := newOccEnv(t)
	tx := occDecodeTx(t, e.confirm(t, nil))
	occWant(t, e.a.Do(t, "DELETE", "/api/v1/transactions/"+tx.ID.String(), nil, e.cookie), 204)
	occWantStatus(t, e, models.OccConfirmed)
	occWantErr(t, e.confirm(t, nil), 409, "OCCURRENCE_NOT_PENDING")
	if n := e.dueLen(t); n != 0 {
		t.Fatalf("due %d", n)
	}
	if b := e.balance(t, e.bca.ID); b != 10000000 {
		t.Fatalf("balance %d", b)
	}
}

func TestConfirm_TransferOccurrence(t *testing.T) {
	e := newOccEnv(t)
	if err := e.a.DB.Model(&models.RecurringOccurrence{}).Where("id = ?", e.occ.ID).
		Updates(map[string]any{"type": "transfer", "to_account_id": e.cash.ID, "category_id": nil, "amount": 500000}).Error; err != nil {
		t.Fatal(err)
	}
	r := e.confirm(t, nil)
	occWant(t, r, 201)
	tx := occDecodeTx(t, r)
	if tx.Type != "transfer" || tx.Amount != 500000 || tx.ToAccount == nil || tx.ToAccount.Name != "Cash" || tx.Category != nil {
		t.Fatalf("dto %+v", tx)
	}
	if b := e.balance(t, e.bca.ID); b != 9500000 {
		t.Fatalf("bca %d", b)
	}
	if b := e.balance(t, e.cash.ID); b != 500000 {
		t.Fatalf("cash %d", b)
	}
}

func TestOccurrence_OtherUser404(t *testing.T) {
	e := newOccEnv(t)
	b, cookieB := e.a.NewUser(t, "b@example.com")
	base := "/api/v1/recurring/occurrences/" + e.occ.ID.String()
	occWant(t, e.a.Do(t, "POST", base+"/confirm", nil, cookieB), 404)
	occWant(t, e.a.Do(t, "POST", base+"/skip", nil, cookieB), 404)
	occWantStatus(t, e, models.OccPending)
	var n int64
	e.a.DB.Model(&models.Transaction{}).Where("user_id IN ?", []uuid.UUID{e.user.ID, b.ID}).Count(&n)
	if n != 0 {
		t.Fatalf("txCount %d", n)
	}
	occWant(t, e.a.Do(t, "POST", "/api/v1/recurring/occurrences/not-a-uuid/confirm", nil, e.cookie), 404)
	occWant(t, e.a.Do(t, "POST", base+"/confirm", nil, ""), 401)
}
