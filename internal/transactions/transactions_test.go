package transactions_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"beprod/internal/jakarta"
	"beprod/internal/models"
	"beprod/internal/testutil"
	"beprod/internal/transactions"
)

const txURL = "/api/v1/transactions"

func txDTO(t *testing.T, r testutil.Response) transactions.DTO {
	t.Helper()
	var out struct {
		Transaction transactions.DTO `json:"transaction"`
	}
	r.Decode(t, &out)
	return out.Transaction
}

func mustStatus(t *testing.T, r testutil.Response, want int) {
	t.Helper()
	if r.Status != want {
		t.Fatalf("status %d (want %d) body %s", r.Status, want, r.Raw)
	}
}

func with(b map[string]any, kv ...any) map[string]any {
	out := map[string]any{}
	for k, v := range b {
		out[k] = v
	}
	for i := 0; i < len(kv); i += 2 {
		if kv[i+1] == nil {
			delete(out, kv[i].(string))
		} else {
			out[kv[i].(string)] = kv[i+1]
		}
	}
	return out
}

func dbTx(t *testing.T, a *testutil.TestApp, id string) models.Transaction {
	t.Helper()
	var m models.Transaction
	if err := a.DB.First(&m, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	return m
}

func archiveAcc(t *testing.T, a *testutil.TestApp, id string) {
	t.Helper()
	if err := a.DB.Model(&models.Account{}).Where("id = ?", id).Update("status", models.StatusArchived).Error; err != nil {
		t.Fatal(err)
	}
}

func archiveCat(t *testing.T, a *testutil.TestApp, id string) {
	t.Helper()
	if err := a.DB.Model(&models.Category{}).Where("id = ?", id).Update("status", models.StatusArchived).Error; err != nil {
		t.Fatal(err)
	}
}

func TestCreate_Expense_ReducesBalance(t *testing.T) {
	a, u, cookie := setup(t)
	cash := apiAccount(t, a, cookie, "Cash", 100000)
	food := catID(t, a, u.ID, "Food & Drinks")
	r := a.Do(t, "POST", txURL, txBody(cash, food, 25000), cookie)
	mustStatus(t, r, 201)
	d := txDTO(t, r)
	if d.Type != "expense" || d.Amount != 25000 || d.Source != "app" || d.Account.Name != "Cash" ||
		d.Category == nil || d.Category.Name != "Food & Drinks" || d.ToAccount != nil ||
		d.Date != "2026-10-20" || d.Time != "14:30" || d.OccurredAt != "2026-10-20T14:30:00+07:00" || d.Account.Archived {
		t.Fatalf("bad dto %+v", d)
	}
	if _, ok := r.Body["transaction"].(map[string]any)["tags"].([]any); !ok {
		t.Fatalf("tags not an array: %s", r.Raw)
	}
	if b := balanceOf(t, a, cookie, cash); b != 75000 {
		t.Fatalf("balance %d", b)
	}
}

func TestCreate_Income_IncreasesBalance(t *testing.T) {
	a, u, cookie := setup(t)
	cash := apiAccount(t, a, cookie, "Cash", 100000)
	r := a.Do(t, "POST", txURL, with(txBody(cash, catID(t, a, u.ID, "Salary"), 50000), "type", "income"), cookie)
	mustStatus(t, r, 201)
	if b := balanceOf(t, a, cookie, cash); b != 150000 {
		t.Fatalf("balance %d", b)
	}
}

func TestCreate_AmountValidation(t *testing.T) {
	a, u, cookie := setup(t)
	cash := apiAccount(t, a, cookie, "Cash", 0)
	food := catID(t, a, u.ID, "Food & Drinks")
	for amt, msg := range map[int64]string{0: "Amount must be greater than 0", -5: "Amount must be greater than 0", 1000000000000: "Amount is too large"} {
		r := a.Do(t, "POST", txURL, txBody(cash, food, amt), cookie)
		if r.Status != 400 || r.ErrCode() != "VALIDATION_ERROR" || r.Field("amount") != msg {
			t.Fatalf("amount %d: %d %s", amt, r.Status, r.Raw)
		}
	}
	mustStatus(t, a.Do(t, "POST", txURL, txBody(cash, food, 999999999999), cookie), 201)
	before := countTxns(t, a, u.ID)
	r := a.Do(t, "POST", txURL, with(txBody(cash, food, 0), "categoryId", ""), cookie)
	if r.Status != 400 || r.Field("categoryId") != "Select a category" || r.Field("amount") == "" {
		t.Fatalf("multi: %s", r.Raw)
	}
	if countTxns(t, a, u.ID) != before {
		t.Fatal("row inserted on validation error")
	}
}

func TestCreate_RequiredFields(t *testing.T) {
	a, u, cookie := setup(t)
	cash := apiAccount(t, a, cookie, "Cash", 0)
	b := txBody(cash, catID(t, a, u.ID, "Food & Drinks"), 100)
	cases := []struct {
		body       map[string]any
		field, msg string
	}{
		{with(b, "type", nil), "type", "Choose expense, income or transfer"},
		{with(b, "type", "bogus"), "type", "Choose expense, income or transfer"},
		{with(b, "accountId", nil), "accountId", "Select an account"},
		{with(b, "accountId", "nope"), "accountId", "This account is no longer available"},
	}
	for _, c := range cases {
		r := a.Do(t, "POST", txURL, c.body, cookie)
		if r.Status != 400 || r.Field(c.field) != c.msg {
			t.Fatalf("%s: %d %s", c.field, r.Status, r.Raw)
		}
	}
}

func TestCreate_DateAndTimeValidation(t *testing.T) {
	a, u, cookie := setup(t)
	cash := apiAccount(t, a, cookie, "Cash", 0)
	b := txBody(cash, catID(t, a, u.ID, "Food & Drinks"), 100)
	cases := []struct {
		body       map[string]any
		field, msg string
	}{
		{with(b, "date", "2026-10-21"), "date", "Date can't be in the future"},
		{with(b, "date", ""), "date", "Select a date"},
		{with(b, "date", "20-10-2026"), "date", "Enter a valid date"},
		{with(b, "time", "7:30"), "time", "Enter a valid time (HH:MM)"},
		{with(b, "time", ""), "time", "Enter a valid time (HH:MM)"},
		{with(b, "time", "24:00"), "time", "Enter a valid time (HH:MM)"},
		{with(b, "date", "bad", "time", "bad"), "time", "Enter a valid time (HH:MM)"},
	}
	for _, c := range cases {
		r := a.Do(t, "POST", txURL, c.body, cookie)
		if r.Status != 400 || r.Field(c.field) != c.msg {
			t.Fatalf("%s %s: %d %s", c.field, c.msg, r.Status, r.Raw)
		}
	}
	mustStatus(t, a.Do(t, "POST", txURL, with(b, "time", "23:59"), cookie), 201)
}

func TestCreate_DateBoundaryAroundMidnightWIB(t *testing.T) {
	a, u, cookie := setup(t)
	cash := apiAccount(t, a, cookie, "Cash", 0)
	b := txBody(cash, catID(t, a, u.ID, "Food & Drinks"), 100)
	testutil.SetNow(t, time.Date(2026, 10, 20, 16, 59, 30, 0, time.UTC))
	mustStatus(t, a.Do(t, "POST", txURL, with(b, "time", "23:59"), cookie), 201)
	r := a.Do(t, "POST", txURL, with(b, "date", "2026-10-21"), cookie)
	if r.Status != 400 || r.Field("date") == "" {
		t.Fatalf("%s", r.Raw)
	}
	testutil.SetNow(t, time.Date(2026, 10, 20, 17, 0, 10, 0, time.UTC))
	r = a.Do(t, "POST", txURL, with(b, "date", "2026-10-21", "time", "00:00"), cookie)
	mustStatus(t, r, 201)
	if d := txDTO(t, r); d.OccurredAt != "2026-10-21T00:00:00+07:00" {
		t.Fatalf("occurredAt %s", d.OccurredAt)
	}
	r = a.Do(t, "POST", txURL, with(b, "date", "2026-10-22"), cookie)
	if r.Status != 400 || r.Field("date") == "" {
		t.Fatalf("%s", r.Raw)
	}
}

func TestCreate_CategoryRules(t *testing.T) {
	a, u, cookie := setup(t)
	cash := apiAccount(t, a, cookie, "Cash", 0)
	u2, _ := a.NewUser(t, "u2@test.local")
	salary := catID(t, a, u.ID, "Salary")
	food := catID(t, a, u.ID, "Food & Drinks")
	transport := catID(t, a, u.ID, "Transport")
	archiveCat(t, a, transport)
	check := func(body map[string]any, msg string) {
		t.Helper()
		r := a.Do(t, "POST", txURL, body, cookie)
		if r.Status != 400 || r.Field("categoryId") != msg {
			t.Fatalf("want %q: %d %s", msg, r.Status, r.Raw)
		}
	}
	check(txBody(cash, salary, 100), "Category doesn't match the transaction type")
	check(with(txBody(cash, food, 100), "type", "income"), "Category doesn't match the transaction type")
	check(txBody(cash, "nope", 100), "This category is no longer available")
	check(txBody(cash, catID(t, a, u2.ID, "Food & Drinks"), 100), "This category is no longer available")
	check(txBody(cash, transport, 100), "This category is no longer available")
}

func TestCreate_AccountRules(t *testing.T) {
	a, u, cookie := setup(t)
	u2, c2 := a.NewUser(t, "u2@test.local")
	other := apiAccount(t, a, c2, "Other", 0)
	food := catID(t, a, u.ID, "Food & Drinks")
	old := apiAccount(t, a, cookie, "Old", 0)
	archiveAcc(t, a, old)
	_ = u2
	for _, id := range []string{other, old} {
		r := a.Do(t, "POST", txURL, txBody(id, food, 100), cookie)
		if r.Status != 400 || r.Field("accountId") != "This account is no longer available" {
			t.Fatalf("%s", r.Raw)
		}
	}
}

func TestUpdate_KeepsArchivedAccountButCannotSwitchToAnother(t *testing.T) {
	a, u, cookie := setup(t)
	cash := apiAccount(t, a, cookie, "Cash", 0)
	bca := apiAccount(t, a, cookie, "BCA", 0)
	old := apiAccount(t, a, cookie, "Old", 0)
	food := catID(t, a, u.ID, "Food & Drinks")
	transport := catID(t, a, u.ID, "Transport")
	drinks := catID(t, a, u.ID, "Shopping")
	id := txID(t, a.Do(t, "POST", txURL, txBody(cash, food, 100), cookie))
	archiveAcc(t, a, cash)
	archiveAcc(t, a, old)
	put := func(body map[string]any) testutil.Response { return a.Do(t, "PUT", txURL+"/"+id, body, cookie) }

	r := put(txBody(cash, food, 200))
	mustStatus(t, r, 200)
	if !txDTO(t, r).Account.Archived {
		t.Fatal("expected archived account ref")
	}
	r = put(txBody(old, food, 200))
	if r.Status != 400 || r.Field("accountId") != "This account is no longer available" {
		t.Fatalf("%s", r.Raw)
	}
	mustStatus(t, put(txBody(bca, food, 200)), 200)

	archiveCat(t, a, food)
	archiveCat(t, a, transport)
	mustStatus(t, put(txBody(bca, food, 300)), 200)
	r = put(txBody(bca, transport, 300))
	if r.Status != 400 || r.Field("categoryId") != "This category is no longer available" {
		t.Fatalf("%s", r.Raw)
	}
	_ = drinks
}

func TestUpdate_ChangeAccountMovesBalance(t *testing.T) {
	a, u, cookie := setup(t)
	cash := apiAccount(t, a, cookie, "Cash", 100000)
	bca := apiAccount(t, a, cookie, "BCA", 200000)
	food := catID(t, a, u.ID, "Food & Drinks")
	r := a.Do(t, "POST", txURL, txBody(cash, food, 25000), cookie)
	first := txDTO(t, r)
	if balanceOf(t, a, cookie, cash) != 75000 || balanceOf(t, a, cookie, bca) != 200000 {
		t.Fatal("setup balances")
	}
	r = a.Do(t, "PUT", txURL+"/"+first.ID.String(), txBody(bca, food, 25000), cookie)
	mustStatus(t, r, 200)
	d := txDTO(t, r)
	if balanceOf(t, a, cookie, cash) != 100000 || balanceOf(t, a, cookie, bca) != 175000 {
		t.Fatal("balances after move")
	}
	if d.Source != "app" || d.ID != first.ID || !d.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("%+v", d)
	}
}

func TestUpdate_FullReplace(t *testing.T) {
	a, u, cookie := setup(t)
	cash := apiAccount(t, a, cookie, "Cash", 0)
	id := txID(t, a.Do(t, "POST", txURL, txBody(cash, catID(t, a, u.ID, "Food & Drinks"), 100), cookie))
	body := map[string]any{"type": "income", "amount": 7777, "accountId": cash, "categoryId": catID(t, a, u.ID, "Salary"),
		"note": "pay", "tags": []string{"#Work"}, "date": "2026-10-19", "time": "08:05"}
	r := a.Do(t, "PUT", txURL+"/"+id, body, cookie)
	mustStatus(t, r, 200)
	d := txDTO(t, r)
	if d.Type != "income" || d.Amount != 7777 || d.Date != "2026-10-19" || d.Time != "08:05" ||
		d.OccurredAt != "2026-10-19T08:05:00+07:00" || d.Note != "pay" || len(d.Tags) != 1 || d.Tags[0] != "work" ||
		d.Category == nil || d.Category.Name != "Salary" {
		t.Fatalf("%+v", d)
	}
}

func TestUpdate_ExpenseToTransferClearsCategory(t *testing.T) {
	a, u, cookie := setup(t)
	cash := apiAccount(t, a, cookie, "Cash", 0)
	bca := apiAccount(t, a, cookie, "BCA", 0)
	food := catID(t, a, u.ID, "Food & Drinks")
	id := txID(t, a.Do(t, "POST", txURL, txBody(cash, food, 100), cookie))
	r := a.Do(t, "PUT", txURL+"/"+id, with(txBody(cash, food, 100), "type", "transfer", "toAccountId", bca), cookie)
	mustStatus(t, r, 200)
	d := txDTO(t, r)
	if d.Category != nil || d.ToAccount == nil || d.ToAccount.Name != "BCA" {
		t.Fatalf("%+v", d)
	}
	if m := dbTx(t, a, id); m.CategoryID != nil {
		t.Fatal("category not cleared in DB")
	}
	r = a.Do(t, "PUT", txURL+"/"+id, txBody(cash, food, 100), cookie)
	mustStatus(t, r, 200)
	if m := dbTx(t, a, id); m.ToAccountID != nil || m.CategoryID == nil {
		t.Fatalf("to_account not cleared: %+v", m)
	}
}

func TestDelete_RestoresBalance(t *testing.T) {
	a, u, cookie := setup(t)
	cash := apiAccount(t, a, cookie, "Cash", 100000)
	id := txID(t, a.Do(t, "POST", txURL, txBody(cash, catID(t, a, u.ID, "Food & Drinks"), 25000), cookie))
	if balanceOf(t, a, cookie, cash) != 75000 {
		t.Fatal("balance")
	}
	mustStatus(t, a.Do(t, "DELETE", txURL+"/"+id, nil, cookie), 204)
	if balanceOf(t, a, cookie, cash) != 100000 {
		t.Fatal("balance not restored")
	}
	mustStatus(t, a.Do(t, "GET", txURL+"/"+id, nil, cookie), 404)
	mustStatus(t, a.Do(t, "DELETE", txURL+"/"+id, nil, cookie), 404)
}

func transferBody(from, to string, amount int64) map[string]any {
	return map[string]any{"type": "transfer", "amount": amount, "accountId": from, "toAccountId": to,
		"date": "2026-10-20", "time": "09:00", "note": "", "tags": []string{}}
}

func TestTransfer_US61_AC1(t *testing.T) {
	a, u, cookie := setup(t)
	bca := apiAccount(t, a, cookie, "BCA", 1000000)
	cash := apiAccount(t, a, cookie, "Cash", 0)
	inc := with(txBody(bca, catID(t, a, u.ID, "Salary"), 500000), "type", "income")
	mustStatus(t, a.Do(t, "POST", txURL, inc, cookie), 201)
	mustStatus(t, a.Do(t, "POST", txURL, txBody(bca, catID(t, a, u.ID, "Food & Drinks"), 100000), cookie), 201)
	if balanceOf(t, a, cookie, bca) != 1400000 {
		t.Fatal("pre balance")
	}
	sum := func() int64 {
		var s int64
		a.DB.Raw("SELECT COALESCE(SUM(amount),0) FROM transactions WHERE user_id = ? AND type IN ('income','expense')", u.ID).Scan(&s)
		return s
	}
	before := sum()
	r := a.Do(t, "POST", txURL, transferBody(bca, cash, 300000), cookie)
	mustStatus(t, r, 201)
	d := txDTO(t, r)
	if d.Type != "transfer" || d.Account.Name != "BCA" || d.ToAccount == nil || d.ToAccount.Name != "Cash" || d.Category != nil {
		t.Fatalf("%+v", d)
	}
	if balanceOf(t, a, cookie, bca) != 1100000 || balanceOf(t, a, cookie, cash) != 300000 {
		t.Fatal("balances after transfer")
	}
	if sum() != before {
		t.Fatal("transfer changed income/expense sum")
	}
}

func TestTransfer_Validation(t *testing.T) {
	a, u, cookie := setup(t)
	bca := apiAccount(t, a, cookie, "BCA", 0)
	cash := apiAccount(t, a, cookie, "Cash", 0)
	_, c2 := a.NewUser(t, "u2@test.local")
	other := apiAccount(t, a, c2, "Other", 0)
	old := apiAccount(t, a, cookie, "Old", 0)
	archiveAcc(t, a, old)
	_ = u
	check := func(body map[string]any, msg string) {
		t.Helper()
		r := a.Do(t, "POST", txURL, body, cookie)
		if r.Status != 400 || r.Field("toAccountId") != msg {
			t.Fatalf("want %q: %d %s", msg, r.Status, r.Raw)
		}
	}
	check(transferBody(bca, bca, 100), "Choose two different accounts.")
	check(with(transferBody(bca, cash, 100), "toAccountId", nil), "Select a destination account")
	check(transferBody(bca, other, 100), "This account is no longer available")
	check(transferBody(bca, old, 100), "This account is no longer available")
	mustStatus(t, a.Do(t, "POST", txURL, transferBody(bca, cash, 100), cookie), 201)
}

func TestTransfer_IgnoresCategoryID(t *testing.T) {
	a, u, cookie := setup(t)
	bca := apiAccount(t, a, cookie, "BCA", 0)
	cash := apiAccount(t, a, cookie, "Cash", 0)
	food := catID(t, a, u.ID, "Food & Drinks")
	r := a.Do(t, "POST", txURL, with(transferBody(bca, cash, 100), "categoryId", food), cookie)
	mustStatus(t, r, 201)
	if txDTO(t, r).Category != nil || dbTx(t, a, txID(t, r)).CategoryID != nil {
		t.Fatal("category should be nil")
	}
	r = a.Do(t, "POST", txURL, with(txBody(bca, food, 100), "toAccountId", cash), cookie)
	mustStatus(t, r, 201)
	if txDTO(t, r).ToAccount != nil || dbTx(t, a, txID(t, r)).ToAccountID != nil {
		t.Fatal("toAccount should be nil")
	}
}

func TestTransfer_DeleteRestoresBothBalances(t *testing.T) {
	a, _, cookie := setup(t)
	bca := apiAccount(t, a, cookie, "BCA", 1000)
	cash := apiAccount(t, a, cookie, "Cash", 50)
	id := txID(t, a.Do(t, "POST", txURL, transferBody(bca, cash, 300), cookie))
	mustStatus(t, a.Do(t, "DELETE", txURL+"/"+id, nil, cookie), 204)
	if balanceOf(t, a, cookie, bca) != 1000 || balanceOf(t, a, cookie, cash) != 50 {
		t.Fatal("balances not restored")
	}
}

func TestTags_Normalized(t *testing.T) {
	a, u, cookie := setup(t)
	cash := apiAccount(t, a, cookie, "Cash", 0)
	b := txBody(cash, catID(t, a, u.ID, "Food & Drinks"), 100)
	r := a.Do(t, "POST", txURL, with(b, "tags", []string{"#Food", "food", "Trip-2026"}), cookie)
	mustStatus(t, r, 201)
	if d := txDTO(t, r); len(d.Tags) != 2 || d.Tags[0] != "food" || d.Tags[1] != "trip-2026" {
		t.Fatalf("%v", d.Tags)
	}
	r = a.Do(t, "POST", txURL, with(b, "tags", []string{"a", "b", "c", "d", "e", "f"}), cookie)
	if r.Status != 400 || r.Field("tags") != "You can add up to 5 tags" {
		t.Fatalf("%s", r.Raw)
	}
	r = a.Do(t, "POST", txURL, with(b, "tags", []string{"bad tag!"}), cookie)
	if r.Status != 400 || r.Field("tags") != "Tags can only use letters, digits and hyphens (max 20 characters)" {
		t.Fatalf("%s", r.Raw)
	}
	r = a.Do(t, "POST", txURL, with(b, "tags", nil), cookie)
	mustStatus(t, r, 201)
	if tg, ok := r.Body["transaction"].(map[string]any)["tags"].([]any); !ok || len(tg) != 0 {
		t.Fatalf("tags %s", r.Raw)
	}
}

func TestNote_Rules(t *testing.T) {
	a, u, cookie := setup(t)
	cash := apiAccount(t, a, cookie, "Cash", 0)
	b := txBody(cash, catID(t, a, u.ID, "Food & Drinks"), 100)
	r := a.Do(t, "POST", txURL, with(b, "note", strings.Repeat("a", 201)), cookie)
	if r.Status != 400 || r.Field("note") != "Note can be at most 200 characters" {
		t.Fatalf("%s", r.Raw)
	}
	mustStatus(t, a.Do(t, "POST", txURL, with(b, "note", strings.Repeat("a", 200)), cookie), 201)
	mustStatus(t, a.Do(t, "POST", txURL, with(b, "note", strings.Repeat("é", 200)), cookie), 201)
	r = a.Do(t, "POST", txURL, with(b, "note", "  hi  "), cookie)
	mustStatus(t, r, 201)
	if txDTO(t, r).Note != "hi" {
		t.Fatal("note not trimmed")
	}
}

func TestCreate_Idempotency(t *testing.T) {
	a, u, cookie := setup(t)
	cash := apiAccount(t, a, cookie, "Cash", 100000)
	b := txBody(cash, catID(t, a, u.ID, "Food & Drinks"), 1000)
	hdr := func(c, k string) map[string]string { return map[string]string{"Cookie": c, "Idempotency-Key": k} }
	r1 := a.DoH(t, "POST", txURL, b, hdr(cookie, "k1"))
	mustStatus(t, r1, 201)
	r2 := a.DoH(t, "POST", txURL, with(b, "amount", 5), hdr(cookie, "k1"))
	mustStatus(t, r2, 200)
	if txID(t, r1) != txID(t, r2) || countTxns(t, a, u.ID) != 1 || balanceOf(t, a, cookie, cash) != 99000 {
		t.Fatal("idempotency broken")
	}
	u2, c2 := a.NewUser(t, "u2@test.local")
	cash2 := apiAccount(t, a, c2, "Cash", 0)
	r3 := a.DoH(t, "POST", txURL, txBody(cash2, catID(t, a, u2.ID, "Food & Drinks"), 100), hdr(c2, "k1"))
	mustStatus(t, r3, 201)
	if txID(t, r3) == txID(t, r1) {
		t.Fatal("ids should differ across users")
	}
	r := a.DoH(t, "POST", txURL, b, hdr(cookie, strings.Repeat("k", 65)))
	if r.Status != 400 || r.ErrCode() != "INVALID_IDEMPOTENCY_KEY" || errMessage(r) != "Idempotency-Key is too long" {
		t.Fatalf("%s", r.Raw)
	}
	mustStatus(t, a.DoH(t, "POST", txURL, b, hdr(cookie, strings.Repeat("k", 64))), 201)
	before := countTxns(t, a, u.ID)
	mustStatus(t, a.Do(t, "POST", txURL, b, cookie), 201)
	mustStatus(t, a.Do(t, "POST", txURL, b, cookie), 201)
	if countTxns(t, a, u.ID) != before+2 {
		t.Fatal("keyless requests should each insert")
	}
}

func TestInsert_NilTagsAndDuplicateKey(t *testing.T) {
	a, u, cookie := setup(t)
	cash := uuid.MustParse(apiAccount(t, a, cookie, "Cash", 0))
	cat := uuid.MustParse(catID(t, a, u.ID, "Food & Drinks"))
	r := transactions.Resolved{Type: "expense", Amount: 1000, AccountID: cash, CategoryID: &cat}
	m, err := transactions.Insert(a.DB, u.ID, r, jakarta.Now(), models.SourceRecurring, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := dbTx(t, a, m.ID.String())
	if got.Tags == nil || len(got.Tags) != 0 || got.Source != "recurring" {
		t.Fatalf("%+v", got)
	}
	key := "dup"
	if _, err := transactions.Insert(a.DB, u.ID, r, jakarta.Now(), models.SourceApp, nil, &key); err != nil {
		t.Fatal(err)
	}
	if _, err := transactions.Insert(a.DB, u.ID, r, jakarta.Now(), models.SourceApp, nil, &key); !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatalf("want ErrDuplicatedKey, got %v", err)
	}
}

func TestOtherUser_Gets404(t *testing.T) {
	a, u, cookie := setup(t)
	cash := apiAccount(t, a, cookie, "Cash", 0)
	b := txBody(cash, catID(t, a, u.ID, "Food & Drinks"), 100)
	id := txID(t, a.Do(t, "POST", txURL, b, cookie))
	_, c2 := a.NewUser(t, "u2@test.local")
	for _, m := range []string{"GET", "PUT", "DELETE"} {
		r := a.Do(t, m, txURL+"/"+id, with(b, "amount", 999), c2)
		if r.Status != 404 || r.ErrCode() != "NOT_FOUND" {
			t.Fatalf("%s: %d %s", m, r.Status, r.Raw)
		}
	}
	if m := dbTx(t, a, id); m.Amount != 100 {
		t.Fatal("row changed")
	}
	mustStatus(t, a.Do(t, "GET", txURL+"/not-a-uuid", nil, cookie), 404)
}

func TestTransactions_RequireLogin(t *testing.T) {
	a, _, _ := setup(t)
	id := uuid.NewString()
	for _, c := range [][2]string{{"POST", txURL}, {"GET", txURL + "/" + id}, {"PUT", txURL + "/" + id}, {"DELETE", txURL + "/" + id}} {
		r := a.Do(t, c[0], c[1], map[string]any{}, "")
		if r.Status != 401 || r.ErrCode() != "UNAUTHENTICATED" {
			t.Fatalf("%s: %d %s", c[0], r.Status, r.Raw)
		}
	}
}
