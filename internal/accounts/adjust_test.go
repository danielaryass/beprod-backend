package accounts_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/lib/pq"

	"beprod/internal/accounts"
	"beprod/internal/models"
	"beprod/internal/testutil"
	"beprod/internal/transactions"
)

type adjResp struct {
	Transaction transactions.DTO `json:"transaction"`
	Account     accounts.DTO     `json:"account"`
}

func adjust(t *testing.T, a *testutil.TestApp, cookie, id string, balance int64) adjResp {
	t.Helper()
	r := a.Do(t, "POST", base+"/"+id+"/adjust", map[string]any{"balance": balance}, cookie)
	if r.Status != 201 {
		t.Fatalf("adjust: status %d body %s", r.Status, r.Raw)
	}
	var out adjResp
	r.Decode(t, &out)
	return out
}

func TestAdjust_UpAndDown(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	acc := apiAccount(t, a, cookie, "BCA", "bank", 1_000_000)
	dbTxn(t, a, u.ID, models.TypeExpense, 100_000, acc.ID, nil)

	out := adjust(t, a, cookie, acc.ID.String(), 950_000)
	tx := out.Transaction
	if tx.Type != "adjustment" || tx.Amount != 50_000 || tx.Category != nil || tx.Note != "Balance adjustment" || tx.Source != "app" {
		t.Fatalf("bad txn %+v", tx)
	}
	if out.Account.Balance != 950_000 || !out.Account.HasTransactions {
		t.Fatalf("bad account %+v", out.Account)
	}
	if got := adjust(t, a, cookie, acc.ID.String(), 900_000).Transaction.Amount; got != -50_000 {
		t.Fatalf("amount %d, want -50000", got)
	}
}

func TestAdjust_Card(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	card := apiAccount(t, a, cookie, "Visa", "credit_card", 0)
	dbTxn(t, a, u.ID, models.TypeExpense, 500_000, card.ID, nil)
	before := getAccount(t, a, cookie, card.ID)

	out := adjust(t, a, cookie, card.ID.String(), -650_000)
	if out.Transaction.Amount != -150_000 {
		t.Fatalf("amount %d", out.Transaction.Amount)
	}
	if out.Account.Card == nil || out.Account.Card.Owed != 650_000 || out.Account.Card.CycleSpend != before.Card.CycleSpend {
		t.Fatalf("card %+v (before %+v)", out.Account.Card, before.Card)
	}
}

func TestAdjust_NotCountedAsIncomeOrExpense(t *testing.T) {
	testutil.SetNow(t, time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC))
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	acc := apiAccount(t, a, cookie, "BCA", "bank", 5_000_000)
	var cat models.Category
	if err := a.DB.Where("user_id = ? AND type = ?", u.ID, models.TypeExpense).First(&cat).Error; err != nil {
		t.Fatal(err)
	}
	if r := a.Do(t, "POST", "/api/v1/budgets", map[string]any{"categoryId": cat.ID, "monthlyLimit": 1_000_000, "rolloverEnabled": false}, cookie); r.Status != 201 {
		t.Fatalf("budget: %d %s", r.Status, r.Raw)
	}
	for _, at := range []time.Time{time.Date(2026, 9, 10, 5, 0, 0, 0, time.UTC), time.Date(2026, 10, 10, 5, 0, 0, 0, time.UTC)} {
		tx := models.Transaction{UserID: u.ID, Type: models.TypeExpense, Amount: 300_000, OccurredAt: at, AccountID: acc.ID,
			CategoryID: &cat.ID, Tags: pq.StringArray{}, Source: models.SourceApp}
		if err := a.DB.Create(&tx).Error; err != nil {
			t.Fatal(err)
		}
	}
	snap := func() string {
		d := a.Do(t, "GET", "/api/v1/dashboard", nil, cookie)
		p := a.Do(t, "GET", "/api/v1/plan", nil, cookie)
		b, _ := json.Marshal([]any{d.Body["summary"], d.Body["breakdown"], d.Body["trend"], d.Body["budgets"], p.Body["emergency"].(map[string]any)["monthlySpend"]})
		return string(b)
	}
	before := snap()
	adjust(t, a, cookie, acc.ID.String(), 4_400_000+200_000)
	if after := snap(); after != before {
		t.Fatalf("adjustment leaked into totals:\nbefore %s\nafter  %s", before, after)
	}
}

func TestAdjust_Errors(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	_, other := a.NewUser(t, "u2@test.local")
	acc := apiAccount(t, a, cookie, "BCA", "bank", 100_000)
	path := base + "/" + acc.ID.String() + "/adjust"

	r := a.Do(t, "POST", path, map[string]any{"balance": 100_000}, cookie)
	if r.Status != 400 || r.ErrCode() != "NO_CHANGE" || errMessage(r) != "Balance already matches" {
		t.Fatalf("no change: %d %s", r.Status, r.Raw)
	}
	for _, b := range []int64{1_000_000_000_000, -1_000_000_000_000} {
		r = a.Do(t, "POST", path, map[string]any{"balance": b}, cookie)
		if r.Status != 400 || r.Field("balance") == "" {
			t.Fatalf("range %d: %d %s", b, r.Status, r.Raw)
		}
	}
	if r = a.Do(t, "POST", path, map[string]any{"balance": 5}, other); r.Status != 404 {
		t.Fatalf("foreign: %d", r.Status)
	}
	if r = a.Do(t, "POST", base+"/00000000-0000-0000-0000-000000000001/adjust", map[string]any{"balance": 5}, cookie); r.Status != 404 {
		t.Fatalf("unknown: %d", r.Status)
	}
	dbTxn(t, a, u.ID, models.TypeExpense, 1, acc.ID, nil) // a used account is archived, not deleted
	if r = a.Do(t, "DELETE", path[:len(path)-len("/adjust")], nil, cookie); r.Status >= 300 {
		t.Fatalf("archive: %d %s", r.Status, r.Raw)
	}
	r = a.Do(t, "POST", path, map[string]any{"balance": 5}, cookie)
	if r.Status != 400 || r.ErrCode() != "ACCOUNT_ARCHIVED" || errMessage(r) != "Unarchive this account first" {
		t.Fatalf("archived: %d %s", r.Status, r.Raw)
	}
}

func TestOpeningBalanceLock(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	acc := apiAccount(t, a, cookie, "BCA", "bank", 100_000)
	path := base + "/" + acc.ID.String()
	if acc.HasTransactions {
		t.Fatal("fresh account reports transactions")
	}
	if r := a.Do(t, "PATCH", path, map[string]any{"openingBalance": 200_000}, cookie); r.Status != 200 {
		t.Fatalf("unlocked change: %d %s", r.Status, r.Raw)
	}
	dbTxn(t, a, u.ID, models.TypeExpense, 1000, acc.ID, nil)
	if !getAccount(t, a, cookie, acc.ID).HasTransactions {
		t.Fatal("hasTransactions false after a transaction")
	}
	r := a.Do(t, "PATCH", path, map[string]any{"openingBalance": 300_000}, cookie)
	if r.Status != 400 || r.Field("openingBalance") != "This account has transactions. Use Adjust balance instead." {
		t.Fatalf("locked: %d %s", r.Status, r.Raw)
	}
	r = a.Do(t, "PATCH", path, map[string]any{"openingBalance": 200_000, "name": "BCA 2"}, cookie)
	if r.Status != 200 || decodeAcc(t, r).Name != "BCA 2" {
		t.Fatalf("same value: %d %s", r.Status, r.Raw)
	}
	// a transfer destination counts too
	dest := apiAccount(t, a, cookie, "Dest", "bank", 0)
	dbTxn(t, a, u.ID, models.TypeTransfer, 1000, acc.ID, &dest.ID)
	if !getAccount(t, a, cookie, dest.ID).HasTransactions {
		t.Fatal("transfer destination should report transactions")
	}
}

func TestAdjustment_ReadOnly(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := a.NewUser(t, "u1@test.local")
	acc := apiAccount(t, a, cookie, "BCA", "bank", 100_000)
	tx := adjust(t, a, cookie, acc.ID.String(), 150_000).Transaction
	path := "/api/v1/transactions/" + tx.ID.String()

	body := map[string]any{"type": "expense", "amount": 1, "accountId": acc.ID, "note": "", "tags": []string{}, "date": "2026-10-20", "time": "10:00"}
	r := a.Do(t, "PUT", path, body, cookie)
	if r.Status != 400 || r.ErrCode() != "ADJUSTMENT_READONLY" || errMessage(r) != "Balance adjustments can't be edited. Delete it and adjust again." {
		t.Fatalf("edit: %d %s", r.Status, r.Raw)
	}
	if r = a.Do(t, "DELETE", path, nil, cookie); r.Status != 204 {
		t.Fatalf("delete: %d", r.Status)
	}
	if b := getAccount(t, a, cookie, acc.ID).Balance; b != 100_000 {
		t.Fatalf("balance %d after delete", b)
	}
}
