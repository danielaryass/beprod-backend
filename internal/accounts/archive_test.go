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

func dbRule(t *testing.T, a *testutil.TestApp, userID, accountID uuid.UUID, toAccountID *uuid.UUID, status, reason string) models.RecurringRule {
	t.Helper()
	typ := models.TypeExpense
	if toAccountID != nil {
		typ = models.TypeTransfer
	}
	r := models.RecurringRule{UserID: userID, Type: typ, Amount: 10000, AccountID: accountID, ToAccountID: toAccountID,
		Tags: pq.StringArray{}, Frequency: models.FreqMonthly, StartDate: jakarta.Today(), NextDueDate: jakarta.Today(),
		Status: status, PausedReason: reason}
	if err := a.DB.Create(&r).Error; err != nil {
		t.Fatalf("insert rule: %v", err)
	}
	return r
}

func accPath(id uuid.UUID, suffix string) string { return base + "/" + id.String() + suffix }

func ruleState(t *testing.T, a *testutil.TestApp, id uuid.UUID) (string, string) {
	t.Helper()
	var r models.RecurringRule
	if err := a.DB.First(&r, "id = ?", id).Error; err != nil {
		t.Fatalf("load rule: %v", err)
	}
	return r.Status, r.PausedReason
}

func accStatus(t *testing.T, a *testutil.TestApp, id uuid.UUID) string {
	t.Helper()
	var acc models.Account
	if err := a.DB.First(&acc, "id = ?", id).Error; err != nil {
		t.Fatalf("load account: %v", err)
	}
	return acc.Status
}

func listAccounts(t *testing.T, a *testutil.TestApp, cookie string) (act, arc []accounts.DTO, total int64) {
	t.Helper()
	r := a.Do(t, "GET", base, nil, cookie)
	var out struct {
		Accounts     []accounts.DTO `json:"accounts"`
		Archived     []accounts.DTO `json:"archived"`
		TotalBalance int64          `json:"totalBalance"`
	}
	r.Decode(t, &out)
	return out.Accounts, out.Archived, out.TotalBalance
}

func TestDeleteAccount_NoHistory_HardDeletes(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := a.NewUser(t, "u1@test.local")
	acc := apiAccount(t, a, cookie, "Cash", "cash", 0)
	r := a.Do(t, "DELETE", accPath(acc.ID, ""), nil, cookie)
	if r.Status != 200 || r.Body["result"] != "deleted" {
		t.Fatalf("status %d %s", r.Status, r.Raw)
	}
	var n int64
	a.DB.Model(&models.Account{}).Where("id = ?", acc.ID).Count(&n)
	if n != 0 {
		t.Fatalf("row still exists")
	}
	if g := a.Do(t, "GET", accPath(acc.ID, ""), nil, cookie); g.Status != 404 {
		t.Fatalf("get status %d", g.Status)
	}
	act, arc, _ := listAccounts(t, a, cookie)
	if len(act) != 0 || len(arc) != 0 {
		t.Fatalf("still listed: %v %v", act, arc)
	}
}

func TestDeleteAccount_WithTransaction_Archives(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	acc := apiAccount(t, a, cookie, "Cash", "cash", 100)
	apiAccount(t, a, cookie, "BCA", "bank", 500)
	dbTxn(t, a, u.ID, models.TypeExpense, 10, acc.ID, nil)
	r := a.Do(t, "DELETE", accPath(acc.ID, ""), nil, cookie)
	if r.Status != 200 || r.Body["result"] != "archived" || r.Body["pausedRules"] != float64(0) {
		t.Fatalf("status %d %s", r.Status, r.Raw)
	}
	if s := accStatus(t, a, acc.ID); s != "archived" {
		t.Fatalf("status %s", s)
	}
	act, arc, total := listAccounts(t, a, cookie)
	if len(act) != 1 || len(arc) != 1 || arc[0].ID != acc.ID || total != 500 {
		t.Fatalf("bad lists %v %v %d", act, arc, total)
	}
}

func TestDeleteAccount_AsTransferDestination_Archives(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	x := apiAccount(t, a, cookie, "A", "bank", 0)
	b := apiAccount(t, a, cookie, "B", "bank", 0)
	c := apiAccount(t, a, cookie, "C", "bank", 0)
	dbTxn(t, a, u.ID, models.TypeTransfer, 10, x.ID, &b.ID)
	if r := a.Do(t, "DELETE", accPath(b.ID, ""), nil, cookie); r.Body["result"] != "archived" {
		t.Fatalf("B: %s", r.Raw)
	}
	if r := a.Do(t, "DELETE", accPath(c.ID, ""), nil, cookie); r.Body["result"] != "deleted" {
		t.Fatalf("C: %s", r.Raw)
	}
}

func TestDeleteAccount_PausesActiveRules(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	x := apiAccount(t, a, cookie, "X", "bank", 0)
	y := apiAccount(t, a, cookie, "Y", "bank", 0)
	z := apiAccount(t, a, cookie, "Z", "bank", 0)
	r1 := dbRule(t, a, u.ID, x.ID, nil, models.StatusActive, "")
	r2 := dbRule(t, a, u.ID, y.ID, &x.ID, models.StatusActive, "")
	r3 := dbRule(t, a, u.ID, x.ID, nil, models.RulePaused, "user")
	r4 := dbRule(t, a, u.ID, x.ID, nil, models.RuleEnded, "")
	r5 := dbRule(t, a, u.ID, z.ID, nil, models.StatusActive, "")
	r := a.Do(t, "DELETE", accPath(x.ID, ""), nil, cookie)
	if r.Status != 200 || r.Body["result"] != "archived" || r.Body["pausedRules"] != float64(2) {
		t.Fatalf("status %d %s", r.Status, r.Raw)
	}
	for _, c := range []struct {
		id          uuid.UUID
		status, why string
	}{
		{r1.ID, "paused", "account_archived"}, {r2.ID, "paused", "account_archived"},
		{r3.ID, "paused", "user"}, {r4.ID, "ended", ""}, {r5.ID, "active", ""},
	} {
		if s, why := ruleState(t, a, c.id); s != c.status || why != c.why {
			t.Fatalf("rule %s: got %s/%s want %s/%s", c.id, s, why, c.status, c.why)
		}
	}
}

func TestDeleteAccount_OnlyPausedRule_StillArchives(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	x := apiAccount(t, a, cookie, "X", "bank", 0)
	dbRule(t, a, u.ID, x.ID, nil, models.RulePaused, "user")
	r := a.Do(t, "DELETE", accPath(x.ID, ""), nil, cookie)
	if r.Body["result"] != "archived" || r.Body["pausedRules"] != float64(0) {
		t.Fatalf("%s", r.Raw)
	}
}

func TestDeleteAccount_AlreadyArchived_IsIdempotent(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	x := dbAccount(t, a, u.ID, "X", 1, models.StatusArchived)
	dbTxn(t, a, u.ID, models.TypeExpense, 10, x.ID, nil)
	r := a.Do(t, "DELETE", accPath(x.ID, ""), nil, cookie)
	if r.Status != 200 || r.Body["result"] != "archived" || r.Body["pausedRules"] != float64(0) {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
}

func TestDeletePreview_Numbers(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	empty := apiAccount(t, a, cookie, "Empty", "bank", 0)
	full := apiAccount(t, a, cookie, "Full", "bank", 0)
	ruleOnly := apiAccount(t, a, cookie, "RuleOnly", "bank", 0)
	dest := apiAccount(t, a, cookie, "Dest", "bank", 0)
	dbTxn(t, a, u.ID, models.TypeExpense, 10, full.ID, nil)
	dbRule(t, a, u.ID, full.ID, nil, models.StatusActive, "")
	dbRule(t, a, u.ID, full.ID, nil, models.StatusActive, "")
	dbRule(t, a, u.ID, full.ID, nil, models.RulePaused, "user")
	dbRule(t, a, u.ID, ruleOnly.ID, nil, models.StatusActive, "")
	dbTxn(t, a, u.ID, models.TypeTransfer, 10, empty.ID, &dest.ID)
	// empty is now a transfer source; use a fresh account for the truly empty case.
	fresh := apiAccount(t, a, cookie, "Fresh", "bank", 0)
	for _, c := range []struct {
		id         uuid.UUID
		will, has  bool
		activeRule float64
	}{
		{fresh.ID, false, false, 0}, {full.ID, true, true, 2}, {ruleOnly.ID, true, false, 1}, {dest.ID, true, true, 0},
	} {
		r := a.Do(t, "GET", accPath(c.id, "/delete-preview"), nil, cookie)
		if r.Status != 200 || r.Body["willArchive"] != c.will || r.Body["hasTransactions"] != c.has || r.Body["activeRecurringRules"] != c.activeRule {
			t.Fatalf("%s: %d %s", c.id, r.Status, r.Raw)
		}
	}
	act, arc, _ := listAccounts(t, a, cookie)
	if len(act) != 5 || len(arc) != 0 {
		t.Fatalf("preview changed data")
	}
}

func TestUnarchive_OK(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	dbAccount(t, a, u.ID, "A", 1, models.StatusActive)
	dbAccount(t, a, u.ID, "B", 2, models.StatusActive)
	c := dbAccount(t, a, u.ID, "C", 9, models.StatusArchived)
	rule := dbRule(t, a, u.ID, c.ID, nil, models.RulePaused, models.PausedAccountArchived)
	r := a.Do(t, "POST", accPath(c.ID, "/unarchive"), nil, cookie)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	got := decodeAcc(t, r)
	if got.Status != "active" || got.SortOrder != 3 {
		t.Fatalf("bad dto %+v", got)
	}
	act, arc, _ := listAccounts(t, a, cookie)
	if len(act) != 3 || len(arc) != 0 {
		t.Fatalf("lists %v %v", act, arc)
	}
	if s, _ := ruleState(t, a, rule.ID); s != "paused" {
		t.Fatalf("rule status %s", s)
	}
}

func TestUnarchive_NameConflict(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	arc := dbAccount(t, a, u.ID, "BCA", 1, models.StatusArchived)
	dbAccount(t, a, u.ID, "bca", 1, models.StatusActive)
	r := a.Do(t, "POST", accPath(arc.ID, "/unarchive"), nil, cookie)
	if r.Status != 409 || r.ErrCode() != "ACCOUNT_NAME_TAKEN" || errMessage(r) != "Rename this account first: an active account already uses this name." {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	if s := accStatus(t, a, arc.ID); s != "archived" {
		t.Fatalf("status %s", s)
	}
}

func TestUnarchive_NotArchived(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := a.NewUser(t, "u1@test.local")
	acc := apiAccount(t, a, cookie, "A", "bank", 0)
	r := a.Do(t, "POST", accPath(acc.ID, "/unarchive"), nil, cookie)
	if r.Status != 400 || r.ErrCode() != "NOT_ARCHIVED" || errMessage(r) != "This account is not archived" {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
}

func TestUnarchive_Limit(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	for i := 0; i < 50; i++ {
		dbAccount(t, a, u.ID, "acc"+string(rune('A'+i/26))+string(rune('a'+i%26)), i+1, models.StatusActive)
	}
	arc := dbAccount(t, a, u.ID, "old", 99, models.StatusArchived)
	r := a.Do(t, "POST", accPath(arc.ID, "/unarchive"), nil, cookie)
	if r.Status != 400 || r.ErrCode() != "ACCOUNT_LIMIT" {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
}

func TestReorder_OK(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	x := dbAccount(t, a, u.ID, "A", 1, models.StatusActive)
	b := dbAccount(t, a, u.ID, "B", 2, models.StatusActive)
	c := dbAccount(t, a, u.ID, "C", 3, models.StatusActive)
	d := dbAccount(t, a, u.ID, "D", 7, models.StatusArchived)
	r := a.Do(t, "PUT", base+"/order", map[string]any{"ids": []string{c.ID.String(), x.ID.String(), b.ID.String()}}, cookie)
	if r.Status != 204 {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	act, arc, _ := listAccounts(t, a, cookie)
	want := []uuid.UUID{c.ID, x.ID, b.ID}
	for i, w := range want {
		if act[i].ID != w || act[i].SortOrder != i+1 {
			t.Fatalf("order %v", act)
		}
	}
	if len(arc) != 1 || arc[0].ID != d.ID || arc[0].SortOrder != 7 {
		t.Fatalf("archived %v", arc)
	}
}

func TestReorder_Invalid(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	other, _ := a.NewUser(t, "u2@test.local")
	x := dbAccount(t, a, u.ID, "A", 1, models.StatusActive)
	b := dbAccount(t, a, u.ID, "B", 2, models.StatusActive)
	arc := dbAccount(t, a, u.ID, "Z", 3, models.StatusArchived)
	foreign := dbAccount(t, a, other.ID, "F", 1, models.StatusActive)
	s := func(ids ...uuid.UUID) []string {
		out := []string{}
		for _, id := range ids {
			out = append(out, id.String())
		}
		return out
	}
	cases := map[string]any{
		"missing":   map[string]any{"ids": s(x.ID)},
		"duplicate": map[string]any{"ids": s(x.ID, x.ID)},
		"foreign":   map[string]any{"ids": s(x.ID, foreign.ID)},
		"archived":  map[string]any{"ids": s(x.ID, arc.ID)},
		"notuuid":   map[string]any{"ids": []string{x.ID.String(), "nope"}},
		"noids":     map[string]any{},
	}
	for name, body := range cases {
		r := a.Do(t, "PUT", base+"/order", body, cookie)
		if r.Status != 400 || r.ErrCode() != "INVALID_ORDER" || errMessage(r) != "The list must contain each active account exactly once" {
			t.Fatalf("%s: %d %s", name, r.Status, r.Raw)
		}
	}
	act, _, _ := listAccounts(t, a, cookie)
	if act[0].ID != x.ID || act[0].SortOrder != 1 || act[1].ID != b.ID || act[1].SortOrder != 2 {
		t.Fatalf("order changed %v", act)
	}
	_, cookie2 := a.NewUser(t, "u3@test.local")
	if r := a.Do(t, "PUT", base+"/order", map[string]any{"ids": []string{}}, cookie2); r.Status != 204 {
		t.Fatalf("empty: %d %s", r.Status, r.Raw)
	}
}

func TestArchiveEndpoints_OtherUserIs404(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie1 := a.NewUser(t, "u1@test.local")
	_, cookie2 := a.NewUser(t, "u2@test.local")
	acc := apiAccount(t, a, cookie1, "A", "bank", 0)
	for _, c := range [][2]string{{"GET", "/delete-preview"}, {"DELETE", ""}, {"POST", "/unarchive"}} {
		r := a.Do(t, c[0], accPath(acc.ID, c[1]), nil, cookie2)
		if r.Status != 404 || r.ErrCode() != "NOT_FOUND" {
			t.Fatalf("%v: %d %s", c, r.Status, r.Raw)
		}
	}
	if got := getAccount(t, a, cookie1, acc.ID); got.Status != "active" {
		t.Fatalf("changed: %+v", got)
	}
}

func TestAccountsOrderRoute_NotShadowedByID(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := a.NewUser(t, "u1@test.local")
	if r := a.Do(t, "PUT", base+"/order", map[string]any{"ids": []string{}}, cookie); r.Status != 204 {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
}

func dbGoal(t *testing.T, a *testutil.TestApp, userID, accountID uuid.UUID, name, status string) {
	t.Helper()
	g := models.Goal{UserID: userID, Name: name, TargetAmount: 1000, AccountID: accountID, Status: status}
	if err := a.DB.Create(&g).Error; err != nil {
		t.Fatalf("insert goal: %v", err)
	}
}

func TestAccountDelete_BlockedByActiveGoal(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	acc := apiAccount(t, a, cookie, "Pocket", "bank", 0)
	dbGoal(t, a, u.ID, acc.ID, "Laptop", models.GoalActive)
	r := a.Do(t, "DELETE", accPath(acc.ID, ""), nil, cookie)
	if r.Status != 409 || r.ErrCode() != "ACCOUNT_HAS_GOAL" || errMessage(r) != `This account is used by goal "Laptop"` {
		t.Fatalf("status %d %s", r.Status, r.Raw)
	}
	if s := accStatus(t, a, acc.ID); s != models.StatusActive {
		t.Fatalf("status = %s", s)
	}
}

func TestAccountDelete_RemovesCompletedGoals(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	acc := apiAccount(t, a, cookie, "Pocket", "bank", 0)
	dbGoal(t, a, u.ID, acc.ID, "Laptop", models.GoalCompleted)
	if r := a.Do(t, "DELETE", accPath(acc.ID, ""), nil, cookie); r.Status != 200 || r.Body["result"] != "deleted" {
		t.Fatalf("status %d %s", r.Status, r.Raw)
	}
	var n int64
	a.DB.Model(&models.Goal{}).Where("account_id = ?", acc.ID).Count(&n)
	if n != 0 {
		t.Fatalf("%d goals left", n)
	}
}
