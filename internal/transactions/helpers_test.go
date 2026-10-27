package transactions_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"beprod/internal/models"
	"beprod/internal/testutil"
)

// 2026-10-20 10:00 WIB
var frozenNow = time.Date(2026, 10, 20, 3, 0, 0, 0, time.UTC)

// setup freezes the clock BEFORE creating the user (so session timestamps are consistent).
func setup(t *testing.T) (*testutil.TestApp, models.User, string) {
	t.Helper()
	a := testutil.NewApp(t)
	testutil.SetNow(t, frozenNow)
	u, cookie := a.NewUser(t, "u1@test.local")
	return a, u, cookie
}

func apiAccount(t *testing.T, a *testutil.TestApp, cookie, name string, opening int64) string {
	t.Helper()
	r := a.Do(t, "POST", "/api/v1/accounts", map[string]any{"name": name, "type": "bank", "openingBalance": opening}, cookie)
	if r.Status != 201 {
		t.Fatalf("create account %q: status %d body %s", name, r.Status, r.Raw)
	}
	acc, _ := r.Body["account"].(map[string]any)
	id, _ := acc["id"].(string)
	return id
}

func catID(t *testing.T, a *testutil.TestApp, userID uuid.UUID, name string) string {
	t.Helper()
	var c models.Category
	if err := a.DB.Where("user_id = ? AND name = ? AND status = ?", userID, name, models.StatusActive).First(&c).Error; err != nil {
		t.Fatalf("category %q: %v", name, err)
	}
	return c.ID.String()
}

func balanceOf(t *testing.T, a *testutil.TestApp, cookie, accountID string) int64 {
	t.Helper()
	r := a.Do(t, "GET", "/api/v1/accounts/"+accountID, nil, cookie)
	var out struct {
		Account struct {
			Balance int64 `json:"balance"`
		} `json:"account"`
	}
	r.Decode(t, &out)
	return out.Account.Balance
}

func txBody(accountID, categoryID string, amount int64) map[string]any {
	return map[string]any{"type": "expense", "amount": amount, "accountId": accountID, "categoryId": categoryID,
		"note": "", "tags": []string{}, "date": "2026-10-20", "time": "14:30"}
}

func txID(t *testing.T, r testutil.Response) string {
	t.Helper()
	tx, _ := r.Body["transaction"].(map[string]any)
	id, _ := tx["id"].(string)
	return id
}

func errMessage(r testutil.Response) string {
	e, _ := r.Body["error"].(map[string]any)
	m, _ := e["message"].(string)
	return m
}

func countTxns(t *testing.T, a *testutil.TestApp, userID uuid.UUID) int64 {
	t.Helper()
	var n int64
	if err := a.DB.Model(&models.Transaction{}).Where("user_id = ?", userID).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}
