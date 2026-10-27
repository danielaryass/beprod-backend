package accounts_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"beprod/internal/accounts"
	"beprod/internal/jakarta"
	"beprod/internal/models"
	"beprod/internal/testutil"
)

func d(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) }

func TestLastStatementDate(t *testing.T) {
	cases := []struct {
		today time.Time
		day   int
		want  time.Time
	}{
		{d(2026, 10, 21), 20, d(2026, 10, 20)},
		{d(2026, 10, 20), 20, d(2026, 9, 20)},
		{d(2026, 10, 5), 20, d(2026, 9, 20)},
		{d(2026, 3, 5), 31, d(2026, 2, 28)},
		{d(2026, 3, 31), 31, d(2026, 2, 28)},
		{d(2026, 4, 1), 31, d(2026, 3, 31)},
	}
	for _, c := range cases {
		if got := accounts.LastStatementDate(c.today, c.day); !got.Equal(c.want) {
			t.Errorf("LastStatementDate(%s, %d) = %s, want %s", c.today.Format("2006-01-02"), c.day, got.Format("2006-01-02"), c.want.Format("2006-01-02"))
		}
	}
}

func TestDueDateAfter(t *testing.T) {
	cases := []struct {
		stmt time.Time
		due  int
		want time.Time
	}{
		{d(2026, 10, 20), 5, d(2026, 11, 5)},
		{d(2026, 10, 20), 25, d(2026, 10, 25)},
		{d(2026, 1, 31), 30, d(2026, 2, 28)},
		{d(2026, 10, 20), 20, d(2026, 11, 20)},
	}
	for _, c := range cases {
		if got := accounts.DueDateAfter(c.stmt, c.due); !got.Equal(c.want) {
			t.Errorf("DueDateAfter(%s, %d) = %s, want %s", c.stmt.Format("2006-01-02"), c.due, got.Format("2006-01-02"), c.want.Format("2006-01-02"))
		}
	}
}

// wib builds an instant in Jakarta time.
func wib(y int, m time.Month, day, h int) time.Time {
	return time.Date(y, m, day, h, 0, 0, 0, jakarta.Loc)
}

func cardTxn(t *testing.T, a *testutil.TestApp, userID uuid.UUID, typ string, amount int64, at time.Time, accountID uuid.UUID, to *uuid.UUID) {
	t.Helper()
	tx := models.Transaction{UserID: userID, Type: typ, Amount: amount, OccurredAt: at, AccountID: accountID,
		ToAccountID: to, Tags: pq.StringArray{}, Source: models.SourceApp}
	if err := a.DB.Create(&tx).Error; err != nil {
		t.Fatalf("insert txn: %v", err)
	}
}

func newCard(t *testing.T, a *testutil.TestApp, cookie string, body map[string]any) accounts.DTO {
	t.Helper()
	body["type"] = "credit_card"
	r := a.Do(t, "POST", base, body, cookie)
	if r.Status != 201 {
		t.Fatalf("create card: %d %s", r.Status, r.Raw)
	}
	return decodeAcc(t, r)
}

func TestCardInfo_Bill(t *testing.T) {
	a := testutil.NewApp(t)
	testutil.SetNow(t, wib(2026, 10, 25, 10))
	u, cookie := a.NewUser(t, "u1@test.local")
	bank := apiAccount(t, a, cookie, "BCA", "bank", 10_000_000)
	card := newCard(t, a, cookie, map[string]any{"name": "Visa", "openingDate": "2026-09-01", "creditLimit": 5_000_000, "statementDay": 20, "dueDay": 5})
	cardTxn(t, a, u.ID, "expense", 300_000, wib(2026, 10, 10, 12), card.ID, nil)
	cardTxn(t, a, u.ID, "expense", 200_000, wib(2026, 10, 20, 23), card.ID, nil)
	cardTxn(t, a, u.ID, "expense", 150_000, wib(2026, 10, 22, 12), card.ID, nil)
	cardTxn(t, a, u.ID, "transfer", 100_000, wib(2026, 10, 23, 12), bank.ID, &card.ID)

	c := getAccount(t, a, cookie, card.ID).Card
	if c == nil || c.Owed != 550_000 || c.Available == nil || *c.Available != 4_450_000 || c.CycleSpend != 150_000 ||
		c.Bill == nil || *c.Bill != 400_000 || c.DueDate == nil || *c.DueDate != "2026-11-05" || c.BillPaid {
		t.Fatalf("card %+v", c)
	}
	if getAccount(t, a, cookie, bank.ID).Card != nil {
		t.Fatal("bank account has card")
	}
}

func TestCardInfo_NoDays(t *testing.T) {
	a := testutil.NewApp(t)
	testutil.SetNow(t, wib(2026, 10, 25, 10))
	u, cookie := a.NewUser(t, "u1@test.local")
	card := newCard(t, a, cookie, map[string]any{"name": "Visa", "openingDate": "2026-09-01"})
	cardTxn(t, a, u.ID, "expense", 999_000, wib(2026, 9, 30, 12), card.ID, nil)
	cardTxn(t, a, u.ID, "expense", 120_000, wib(2026, 10, 3, 12), card.ID, nil)
	cardTxn(t, a, u.ID, "expense", 80_000, wib(2026, 10, 24, 12), card.ID, nil)

	c := getAccount(t, a, cookie, card.ID).Card
	if c == nil || c.Bill != nil || c.DueDate != nil || c.Available != nil || c.CycleSpend != 200_000 {
		t.Fatalf("card %+v", c)
	}
}

func TestCardInfo_OpenedAfterStatement(t *testing.T) {
	a := testutil.NewApp(t)
	testutil.SetNow(t, wib(2026, 10, 25, 10))
	_, cookie := a.NewUser(t, "u1@test.local")
	card := newCard(t, a, cookie, map[string]any{"name": "Visa", "openingDate": "2026-10-22", "openingBalance": -500_000, "statementDay": 20, "dueDay": 5})

	c := getAccount(t, a, cookie, card.ID).Card
	if c == nil || c.Bill == nil || *c.Bill != 0 || !c.BillPaid {
		t.Fatalf("card %+v", c)
	}
}

func TestCardInfo_Overpaid(t *testing.T) {
	a := testutil.NewApp(t)
	testutil.SetNow(t, wib(2026, 10, 25, 10))
	_, cookie := a.NewUser(t, "u1@test.local")
	card := newCard(t, a, cookie, map[string]any{"name": "Visa", "openingDate": "2026-09-01", "openingBalance": 200_000, "creditLimit": 1_000_000, "statementDay": 20, "dueDay": 5})

	c := getAccount(t, a, cookie, card.ID).Card
	if c == nil || c.Owed != 0 || c.Available == nil || *c.Available != 1_000_000 || c.Bill == nil || *c.Bill != 0 || !c.BillPaid {
		t.Fatalf("card %+v", c)
	}
}

func TestAccount_CardFieldsPatchKeepsValues(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := a.NewUser(t, "u1@test.local")
	card := newCard(t, a, cookie, map[string]any{"name": "Visa", "creditLimit": 2_000_000, "statementDay": 20, "dueDay": 5})
	if card.StatementDay != 20 || card.DueDay != 5 || card.CreditLimit != 2_000_000 || card.Card == nil {
		t.Fatalf("create %+v", card)
	}
	r := a.Do(t, "PATCH", base+"/"+card.ID.String(), map[string]any{"name": "X"}, cookie)
	if r.Status != 200 {
		t.Fatalf("patch %d %s", r.Status, r.Raw)
	}
	if got := decodeAcc(t, r); got.StatementDay != 20 || got.DueDay != 5 || got.CreditLimit != 2_000_000 || got.Card == nil {
		t.Fatalf("patch kept %+v", got)
	}
	r = a.Do(t, "PATCH", base+"/"+card.ID.String(), map[string]any{"dueDay": 32}, cookie)
	if r.Status != 422 && r.Status != 400 || r.Field("dueDay") != "Choose a day from 1 to 31, or leave empty" {
		t.Fatalf("dueDay 32: %d %s", r.Status, r.Raw)
	}
	r = a.Do(t, "PATCH", base+"/"+card.ID.String(), map[string]any{"creditLimit": 1_000_000_000_000}, cookie)
	if r.Field("creditLimit") != "Credit limit is out of range" {
		t.Fatalf("limit: %d %s", r.Status, r.Raw)
	}
	if bank := apiAccount(t, a, cookie, "BCA", "bank", 0); bank.Card != nil {
		t.Fatalf("bank card %+v", bank.Card)
	}
}
