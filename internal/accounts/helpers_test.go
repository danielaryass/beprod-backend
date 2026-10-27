package accounts_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"beprod/internal/accounts"
	"beprod/internal/jakarta"
	"beprod/internal/models"
	"beprod/internal/testutil"
)

// apiAccount creates an account through POST /accounts and returns it (fails the test unless 201).
func apiAccount(t *testing.T, a *testutil.TestApp, cookie, name, typ string, opening int64) accounts.DTO {
	t.Helper()
	r := a.Do(t, "POST", "/api/v1/accounts", map[string]any{"name": name, "type": typ, "openingBalance": opening}, cookie)
	if r.Status != 201 {
		t.Fatalf("create account %q: status %d body %s", name, r.Status, r.Raw)
	}
	var out struct {
		Account accounts.DTO `json:"account"`
	}
	r.Decode(t, &out)
	return out.Account
}

// dbAccount inserts an account directly (bypasses the API and its limits).
func dbAccount(t *testing.T, a *testutil.TestApp, userID uuid.UUID, name string, sortOrder int, status string) models.Account {
	t.Helper()
	acc := models.Account{UserID: userID, Name: name, Type: models.AccountBank, OpeningDate: jakarta.Today(), SortOrder: sortOrder, Status: status}
	if err := a.DB.Create(&acc).Error; err != nil {
		t.Fatalf("insert account: %v", err)
	}
	return acc
}

// dbTxn inserts a transaction directly (no category, no tags, source app, occurred now).
func dbTxn(t *testing.T, a *testutil.TestApp, userID uuid.UUID, typ string, amount int64, accountID uuid.UUID, toAccountID *uuid.UUID) models.Transaction {
	t.Helper()
	tx := models.Transaction{UserID: userID, Type: typ, Amount: amount, OccurredAt: jakarta.Now(), AccountID: accountID,
		ToAccountID: toAccountID, Tags: pq.StringArray{}, Source: models.SourceApp}
	if err := a.DB.Create(&tx).Error; err != nil {
		t.Fatalf("insert txn: %v", err)
	}
	return tx
}

func getAccount(t *testing.T, a *testutil.TestApp, cookie string, id uuid.UUID) accounts.DTO {
	t.Helper()
	r := a.Do(t, "GET", "/api/v1/accounts/"+id.String(), nil, cookie)
	if r.Status != 200 {
		t.Fatalf("get account: status %d body %s", r.Status, r.Raw)
	}
	var out struct {
		Account accounts.DTO `json:"account"`
	}
	r.Decode(t, &out)
	return out.Account
}

func errMessage(r testutil.Response) string {
	e, _ := r.Body["error"].(map[string]any)
	m, _ := e["message"].(string)
	return m
}
