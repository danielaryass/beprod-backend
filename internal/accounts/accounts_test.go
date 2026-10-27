package accounts_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"beprod/internal/accounts"
	"beprod/internal/models"
	"beprod/internal/testutil"
)

const base = "/api/v1/accounts"

func post(t *testing.T, a *testutil.TestApp, cookie string, body map[string]any) testutil.Response {
	t.Helper()
	return a.Do(t, "POST", base, body, cookie)
}

func decodeAcc(t *testing.T, r testutil.Response) accounts.DTO {
	t.Helper()
	var out struct {
		Account accounts.DTO `json:"account"`
	}
	r.Decode(t, &out)
	return out.Account
}

func TestCreateAccount_OK(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := a.NewUser(t, "u1@test.local")
	r := post(t, a, cookie, map[string]any{"name": "BCA", "type": "bank", "openingBalance": 5000000})
	if r.Status != 201 {
		t.Fatalf("status %d %s", r.Status, r.Raw)
	}
	acc := decodeAcc(t, r)
	if acc.Balance != 5000000 || acc.OpeningBalance != 5000000 || acc.Status != "active" || acc.SortOrder != 1 || acc.Owed {
		t.Fatalf("bad dto %+v", acc)
	}
	if b := apiAccount(t, a, cookie, "Cash", "cash", 0); b.SortOrder != 2 {
		t.Fatalf("sortOrder %d", b.SortOrder)
	}
}

func TestCreateAccount_CreditCardNegative(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := a.NewUser(t, "u1@test.local")
	cc := apiAccount(t, a, cookie, "Visa", "credit_card", -2000000)
	if cc.Balance != -2000000 || !cc.Owed {
		t.Fatalf("cc %+v", cc)
	}
	if b := apiAccount(t, a, cookie, "BCA", "bank", -100); b.Owed {
		t.Fatalf("bank owed %+v", b)
	}
}

func TestCreateAccount_DuplicateName(t *testing.T) {
	a := testutil.NewApp(t)
	_, c1 := a.NewUser(t, "u1@test.local")
	_, c2 := a.NewUser(t, "u2@test.local")
	apiAccount(t, a, c1, "BCA", "bank", 0)
	r := post(t, a, c1, map[string]any{"name": "bca", "type": "bank"})
	if r.Status != 409 || r.ErrCode() != "ACCOUNT_NAME_TAKEN" || errMessage(r) != "You already have an account with this name." {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
	apiAccount(t, a, c2, "BCA", "bank", 0)
}

func TestCreateAccount_FutureOpeningDate(t *testing.T) {
	a := testutil.NewApp(t)
	testutil.SetNow(t, time.Date(2026, 10, 20, 3, 0, 0, 0, time.UTC))
	_, cookie := a.NewUser(t, "u1@test.local")
	r := post(t, a, cookie, map[string]any{"name": "A", "type": "bank", "openingDate": "2026-10-21"})
	if r.Status != 400 || r.Field("openingDate") != "Opening date can't be in the future" {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
	r = post(t, a, cookie, map[string]any{"name": "A", "type": "bank", "openingDate": "2026-10-20"})
	if r.Status != 201 || decodeAcc(t, r).OpeningDate != "2026-10-20" {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
	r = post(t, a, cookie, map[string]any{"name": "B", "type": "bank", "openingDate": "20/10/2026"})
	if r.Status != 400 || r.Field("openingDate") != "Enter a valid date" {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
}

func TestCreateAccount_DefaultOpeningDateIsToday(t *testing.T) {
	a := testutil.NewApp(t)
	testutil.SetNow(t, time.Date(2026, 10, 20, 17, 30, 0, 0, time.UTC))
	_, cookie := a.NewUser(t, "u1@test.local")
	r := post(t, a, cookie, map[string]any{"name": "A", "type": "bank"})
	if r.Status != 201 || decodeAcc(t, r).OpeningDate != "2026-10-21" {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
}

func TestCreateAccount_Validation(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := a.NewUser(t, "u1@test.local")
	bad := func(body map[string]any, field, msg string) {
		t.Helper()
		r := post(t, a, cookie, body)
		if r.Status != 400 || r.ErrCode() != "VALIDATION_ERROR" || r.Field(field) != msg {
			t.Fatalf("%v: got %d %s", body, r.Status, r.Raw)
		}
	}
	const nameMsg = "Name must be 1–40 characters"
	const typeMsg = "Choose a valid account type"
	const balMsg = "Opening balance is out of range"
	bad(map[string]any{"name": "", "type": "bank"}, "name", nameMsg)
	bad(map[string]any{"name": "   ", "type": "bank"}, "name", nameMsg)
	bad(map[string]any{"name": strings.Repeat("a", 41), "type": "bank"}, "name", nameMsg)
	bad(map[string]any{"name": "X", "type": "savings"}, "type", typeMsg)
	bad(map[string]any{"name": "X", "type": ""}, "type", typeMsg)
	bad(map[string]any{"name": "X", "type": "bank", "openingBalance": 1000000000000}, "openingBalance", balMsg)
	bad(map[string]any{"name": "X", "type": "bank", "openingBalance": -1000000000000}, "openingBalance", balMsg)

	for _, body := range []map[string]any{
		{"name": strings.Repeat("é", 40), "type": "bank"},
		{"name": "Max", "type": "bank", "openingBalance": 999999999999},
	} {
		if r := post(t, a, cookie, body); r.Status != 201 {
			t.Fatalf("%v: got %d %s", body, r.Status, r.Raw)
		}
	}
	r := post(t, a, cookie, map[string]any{"name": "  Cash  ", "type": "cash"})
	if r.Status != 201 || decodeAcc(t, r).Name != "Cash" {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
}

func TestCreateAccount_ActiveLimit(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	for i := 1; i <= 50; i++ {
		dbAccount(t, a, u.ID, fmt.Sprintf("Acc %d", i), i, "active")
	}
	r := post(t, a, cookie, map[string]any{"name": "Extra", "type": "bank"})
	if r.Status != 400 || r.ErrCode() != "ACCOUNT_LIMIT" || errMessage(r) != "You can have at most 50 active accounts." {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
	u2, c2 := a.NewUser(t, "u2@test.local")
	for i := 1; i <= 49; i++ {
		dbAccount(t, a, u2.ID, fmt.Sprintf("Acc %d", i), i, "active")
	}
	for i := 1; i <= 5; i++ {
		dbAccount(t, a, u2.ID, fmt.Sprintf("Old %d", i), i, "archived")
	}
	if r := post(t, a, c2, map[string]any{"name": "Extra", "type": "bank"}); r.Status != 201 {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
}

func TestAccounts_RequireLogin(t *testing.T) {
	a := testutil.NewApp(t)
	r := a.Do(t, "GET", base, nil, "")
	if r.Status != 401 || r.ErrCode() != "UNAUTHENTICATED" {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
}

func TestBalance_Math(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	A := apiAccount(t, a, cookie, "A", "bank", 1000000)
	B := apiAccount(t, a, cookie, "B", "cash", 0)
	dbTxn(t, a, u.ID, models.TypeIncome, 500000, A.ID, nil)
	dbTxn(t, a, u.ID, models.TypeExpense, 200000, A.ID, nil)
	dbTxn(t, a, u.ID, models.TypeTransfer, 300000, A.ID, &B.ID)
	dbTxn(t, a, u.ID, models.TypeExpense, 2000000, B.ID, nil)
	if got := getAccount(t, a, cookie, A.ID).Balance; got != 1000000 {
		t.Fatalf("A = %d", got)
	}
	if got := getAccount(t, a, cookie, B.ID).Balance; got != -1700000 {
		t.Fatalf("B = %d", got)
	}
	r := a.Do(t, "GET", base, nil, cookie)
	if r.Body["totalBalance"] != float64(-700000) {
		t.Fatalf("total %v", r.Body["totalBalance"])
	}
}

func TestBalance_OpeningBalanceOnlyAndEmptyUser(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := a.NewUser(t, "u1@test.local")
	r := a.Do(t, "GET", base, nil, cookie)
	acc, ok1 := r.Body["accounts"].([]any)
	arc, ok2 := r.Body["archived"].([]any)
	if r.Status != 200 || !ok1 || !ok2 || len(acc) != 0 || len(arc) != 0 || r.Body["totalBalance"] != float64(0) {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
}

func TestUpdateAccount_OpeningBalanceRecalculates(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	acc := apiAccount(t, a, cookie, "A", "bank", 100000)
	url := base + "/" + acc.ID.String()
	// the opening balance is locked once the account has transactions, so change it first
	r := a.Do(t, "PATCH", url, map[string]any{"openingBalance": 200000}, cookie)
	if r.Status != 200 || decodeAcc(t, r).Balance != 200000 {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
	dbTxn(t, a, u.ID, models.TypeExpense, 30000, acc.ID, nil)
	if g := getAccount(t, a, cookie, acc.ID); g.OpeningBalance != 200000 || g.Balance != 170000 {
		t.Fatalf("get %+v", g)
	}
}

func TestUpdateAccount_Fields(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := a.NewUser(t, "u1@test.local")
	acc := apiAccount(t, a, cookie, "Old", "bank", 10)
	url := base + "/" + acc.ID.String()
	r := a.Do(t, "PATCH", url, map[string]any{"name": "  New  ", "type": "ewallet"}, cookie)
	d := decodeAcc(t, r)
	if r.Status != 200 || d.Name != "New" || d.Type != "ewallet" || d.SortOrder != acc.SortOrder || d.Status != acc.Status {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
	r = a.Do(t, "PATCH", url, map[string]any{}, cookie)
	if r.Status != 200 || decodeAcc(t, r) != d {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
	r = a.Do(t, "PATCH", url, map[string]any{"name": ""}, cookie)
	if r.Status != 400 || r.Field("name") == "" {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
	r = a.Do(t, "PATCH", url, map[string]any{"openingDate": "2999-01-01"}, cookie)
	if r.Status != 400 || r.Field("openingDate") == "" {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
}

func TestUpdateAccount_DuplicateName(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := a.NewUser(t, "u1@test.local")
	bca := apiAccount(t, a, cookie, "BCA", "bank", 0)
	cash := apiAccount(t, a, cookie, "Cash", "cash", 0)
	r := a.Do(t, "PATCH", base+"/"+cash.ID.String(), map[string]any{"name": "bca"}, cookie)
	if r.Status != 409 || r.ErrCode() != "ACCOUNT_NAME_TAKEN" {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
	r = a.Do(t, "PATCH", base+"/"+bca.ID.String(), map[string]any{"name": "bca"}, cookie)
	if r.Status != 200 {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
}

func TestListAccounts_OrderingAndTotal(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	for _, x := range []struct {
		name string
		sort int
		bal  int64
	}{{"C", 3, 300}, {"A", 1, 100}, {"B", 2, 200}} {
		acc := dbAccount(t, a, u.ID, x.name, x.sort, "active")
		a.DB.Model(&acc).Update("opening_balance", x.bal)
	}
	for _, n := range []string{"Zeta", "Alpha"} {
		acc := dbAccount(t, a, u.ID, n, 0, "archived")
		a.DB.Model(&acc).Update("opening_balance", 1000)
	}
	r := a.Do(t, "GET", base, nil, cookie)
	var out struct {
		Accounts     []accounts.DTO `json:"accounts"`
		Archived     []accounts.DTO `json:"archived"`
		TotalBalance int64          `json:"totalBalance"`
	}
	r.Decode(t, &out)
	names := func(l []accounts.DTO) string {
		s := ""
		for _, d := range l {
			s += d.Name
		}
		return s
	}
	if names(out.Accounts) != "ABC" || names(out.Archived) != "AlphaZeta" || out.TotalBalance != 600 {
		t.Fatalf("got %s", r.Raw)
	}
}

func TestGetUpdateAccount_OtherUserIs404(t *testing.T) {
	a := testutil.NewApp(t)
	_, c1 := a.NewUser(t, "u1@test.local")
	_, c2 := a.NewUser(t, "u2@test.local")
	acc := apiAccount(t, a, c1, "Mine", "bank", 0)
	url := base + "/" + acc.ID.String()
	for _, m := range []string{"GET", "PATCH"} {
		var body any
		if m == "PATCH" {
			body = map[string]any{"name": "Hacked"}
		}
		r := a.Do(t, m, url, body, c2)
		if r.Status != 404 || r.ErrCode() != "NOT_FOUND" {
			t.Fatalf("%s: got %d %s", m, r.Status, r.Raw)
		}
	}
	if got := getAccount(t, a, c1, acc.ID).Name; got != "Mine" {
		t.Fatalf("name changed to %q", got)
	}
	if r := a.Do(t, "GET", base+"/not-a-uuid", nil, c2); r.Status != 404 {
		t.Fatalf("got %d", r.Status)
	}
	r := a.Do(t, "GET", base, nil, c2)
	if l, _ := r.Body["accounts"].([]any); len(l) != 0 {
		t.Fatalf("leak %s", r.Raw)
	}
}
