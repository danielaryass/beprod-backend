package goals_test

import (
	"testing"
	"time"

	"beprod/internal/testutil"
)

const base = "/api/v1/goals"

type env struct {
	a      *testutil.TestApp
	cookie string
}

func setup(t *testing.T) env {
	t.Helper()
	testutil.SetNow(t, time.Date(2026, 10, 23, 5, 0, 0, 0, time.UTC))
	a := testutil.NewApp(t)
	_, cookie := a.NewUser(t, "u@example.com")
	return env{a, cookie}
}

func (e env) account(t *testing.T, name, typ string, opening int64) string {
	t.Helper()
	r := e.a.Do(t, "POST", "/api/v1/accounts", map[string]any{"name": name, "type": typ, "openingBalance": opening, "openingDate": "2026-09-01"}, e.cookie)
	if r.Status != 201 {
		t.Fatalf("create account: %d %s", r.Status, r.Raw)
	}
	return r.Body["account"].(map[string]any)["id"].(string)
}

func (e env) goal(t *testing.T, body map[string]any, want int) map[string]any {
	t.Helper()
	r := e.a.Do(t, "POST", base, body, e.cookie)
	if r.Status != want {
		t.Fatalf("create goal: %d, want %d: %s", r.Status, want, r.Raw)
	}
	g, _ := r.Body["goal"].(map[string]any)
	return g
}

func TestGoals_CreateListProgress(t *testing.T) {
	e := setup(t)
	pocket := e.account(t, "Pocket", "bank", 2_000_000)
	bank := e.account(t, "Bank", "bank", 5_000_000)
	r := e.a.Do(t, "POST", "/api/v1/transactions", map[string]any{"type": "transfer", "amount": 1_000_000, "accountId": bank,
		"toAccountId": pocket, "note": "", "tags": []string{}, "date": "2026-10-05", "time": "10:00"}, e.cookie)
	if r.Status != 201 {
		t.Fatalf("transfer: %d %s", r.Status, r.Raw)
	}
	g := e.goal(t, map[string]any{"name": "Laptop", "targetAmount": 12_000_000, "targetDate": "2027-03-15", "accountId": pocket}, 201)
	want := map[string]any{"saved": 3_000_000.0, "percent": 25.0, "monthly": 1_666_667.0, "unsavedThisMonth": 666_667.0,
		"reached": false, "status": "active", "accountName": "Pocket", "targetDate": "2027-03-15", "completedAt": nil}
	for k, v := range want {
		if g[k] != v {
			t.Errorf("%s = %v, want %v", k, g[k], v)
		}
	}
	l := e.a.Do(t, "GET", base, nil, e.cookie)
	if gs, _ := l.Body["goals"].([]any); len(gs) != 1 {
		t.Fatalf("list: %s", l.Raw)
	}
	if cs, ok := l.Body["completed"].([]any); !ok || len(cs) != 0 {
		t.Fatalf("completed: %s", l.Raw)
	}
}

func TestGoals_Validation(t *testing.T) {
	e := setup(t)
	acc := e.account(t, "Pocket", "bank", 0)
	card := e.account(t, "Card", "credit_card", 0)
	e.goal(t, map[string]any{"name": "First", "targetAmount": 1000, "accountId": acc}, 201)
	cases := []struct {
		name  string
		body  map[string]any
		field string
		msg   string
	}{
		{"empty name", map[string]any{"name": " ", "targetAmount": 1, "accountId": acc}, "name", "Name must be 1–40 characters"},
		{"long name", map[string]any{"name": "12345678901234567890123456789012345678901", "targetAmount": 1, "accountId": acc}, "name", "Name must be 1–40 characters"},
		{"zero amount", map[string]any{"name": "X", "targetAmount": 0, "accountId": acc}, "targetAmount", "Enter an amount above 0"},
		{"past date", map[string]any{"name": "X", "targetAmount": 1, "targetDate": "2026-10-23", "accountId": acc}, "targetDate", "Target date must be in the future"},
		{"bad date", map[string]any{"name": "X", "targetAmount": 1, "targetDate": "soon", "accountId": acc}, "targetDate", "Target date must be in the future"},
		{"no account", map[string]any{"name": "X", "targetAmount": 1}, "accountId", "Choose one of your accounts"},
		{"unknown account", map[string]any{"name": "X", "targetAmount": 1, "accountId": "00000000-0000-0000-0000-000000000001"}, "accountId", "Choose one of your accounts"},
		{"card", map[string]any{"name": "X", "targetAmount": 1, "accountId": card}, "accountId", "Credit cards can't hold savings"},
		{"taken", map[string]any{"name": "X", "targetAmount": 1, "accountId": acc}, "accountId", "This account already has a goal"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := e.a.Do(t, "POST", base, c.body, e.cookie)
			if r.Status != 400 || r.ErrCode() != "VALIDATION_ERROR" || r.Field(c.field) != c.msg {
				t.Errorf("%d %s, want 400 %s=%q", r.Status, r.Raw, c.field, c.msg)
			}
		})
	}
	archived := e.account(t, "Old", "bank", 0)
	e.a.Do(t, "DELETE", "/api/v1/accounts/"+archived, nil, e.cookie) // no history: deleted, so also unknown
	if r := e.a.Do(t, "POST", base, map[string]any{"name": "X", "targetAmount": 1, "accountId": archived}, e.cookie); r.Field("accountId") != "Choose one of your accounts" {
		t.Errorf("deleted account: %s", r.Raw)
	}
}

func TestGoals_PatchClearsDate(t *testing.T) {
	e := setup(t)
	acc := e.account(t, "Pocket", "bank", 0)
	g := e.goal(t, map[string]any{"name": "X", "targetAmount": 1000, "targetDate": "2027-01-01", "accountId": acc}, 201)
	r := e.a.Do(t, "PATCH", base+"/"+g["id"].(string), map[string]any{"name": "Y"}, e.cookie)
	if r.Status != 200 || r.Body["goal"].(map[string]any)["targetDate"] != "2027-01-01" {
		t.Fatalf("keep: %d %s", r.Status, r.Raw)
	}
	r = e.a.Do(t, "PATCH", base+"/"+g["id"].(string), map[string]any{"targetDate": nil}, e.cookie)
	if gg := r.Body["goal"].(map[string]any); r.Status != 200 || gg["targetDate"] != nil || gg["name"] != "Y" || gg["monthly"] != nil {
		t.Fatalf("clear: %d %s", r.Status, r.Raw)
	}
}

func TestGoals_CompleteThenAccountReusable(t *testing.T) {
	e := setup(t)
	acc := e.account(t, "Pocket", "bank", 0)
	g := e.goal(t, map[string]any{"name": "X", "targetAmount": 1000, "accountId": acc}, 201)
	id := g["id"].(string)
	r := e.a.Do(t, "POST", base+"/"+id+"/complete", nil, e.cookie)
	if gg, _ := r.Body["goal"].(map[string]any); r.Status != 200 || gg["status"] != "completed" || gg["completedAt"] == nil {
		t.Fatalf("complete: %d %s", r.Status, r.Raw)
	}
	l := e.a.Do(t, "GET", base, nil, e.cookie)
	if len(l.Body["goals"].([]any)) != 0 || len(l.Body["completed"].([]any)) != 1 {
		t.Fatalf("list: %s", l.Raw)
	}
	e.goal(t, map[string]any{"name": "Next", "targetAmount": 2000, "accountId": acc}, 201)
	r = e.a.Do(t, "PATCH", base+"/"+id, map[string]any{"name": "Z"}, e.cookie)
	if r.Status != 400 || r.ErrCode() != "GOAL_COMPLETED" {
		t.Fatalf("patch completed: %d %s", r.Status, r.Raw)
	}
}

func TestGoals_Delete(t *testing.T) {
	e := setup(t)
	acc := e.account(t, "Pocket", "bank", 0)
	id := e.goal(t, map[string]any{"name": "X", "targetAmount": 1000, "accountId": acc}, 201)["id"].(string)
	if r := e.a.Do(t, "DELETE", base+"/"+id, nil, e.cookie); r.Status != 204 {
		t.Fatalf("delete: %d %s", r.Status, r.Raw)
	}
	if r := e.a.Do(t, "DELETE", base+"/"+id, nil, e.cookie); r.Status != 404 {
		t.Fatalf("delete again: %d", r.Status)
	}
}
