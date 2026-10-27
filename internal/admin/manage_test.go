package admin_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"beprod/internal/auth"
	"beprod/internal/jakarta"
	"beprod/internal/models"
	"beprod/internal/testutil"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

func insertUser(t *testing.T, a *testutil.TestApp, name, email string, createdAt time.Time) models.User {
	t.Helper()
	u := models.User{Name: name, Email: email, Role: models.RoleUser, Status: models.UserActive, CreatedAt: createdAt}
	if err := a.DB.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	return u
}

func insertTx(t *testing.T, a *testutil.TestApp, userID uuid.UUID, occurredAt time.Time) {
	t.Helper()
	tx := models.Transaction{UserID: userID, Type: models.TypeExpense, Amount: 1000, OccurredAt: occurredAt,
		AccountID: uuid.New(), Source: models.SourceApp, Tags: pq.StringArray{}, Note: "secret lunch"}
	if err := a.DB.Create(&tx).Error; err != nil {
		t.Fatal(err)
	}
}

func listUsersReq(t *testing.T, a *testutil.TestApp, cookie, query string) testutil.Response {
	t.Helper()
	return a.Do(t, "GET", "/api/v1/admin/users"+query, nil, cookie)
}

func auditCount(t *testing.T, a *testutil.TestApp, adminID, targetID uuid.UUID, action string) int64 {
	t.Helper()
	var n int64
	a.DB.Model(&models.AuditLog{}).Where("admin_id = ? AND target_user_id = ? AND action = ?", adminID, targetID, action).Count(&n)
	return n
}

func listOf(t *testing.T, r testutil.Response) []map[string]any {
	t.Helper()
	if r.Status != 200 {
		t.Fatalf("list: %d %s", r.Status, r.Raw)
	}
	raw, ok := r.Body["users"].([]any)
	if !ok {
		t.Fatalf("users is not an array: %s", r.Raw)
	}
	out := make([]map[string]any, len(raw))
	for i, v := range raw {
		out[i] = v.(map[string]any)
	}
	return out
}

func emailsOf(rows []map[string]any) []string {
	out := make([]string, len(rows))
	for i, m := range rows {
		out[i] = m["email"].(string)
	}
	return out
}

func rowByEmail(t *testing.T, rows []map[string]any, email string) map[string]any {
	t.Helper()
	for _, m := range rows {
		if m["email"] == email {
			return m
		}
	}
	t.Fatalf("no row for %s", email)
	return nil
}

func TestListUsers_NewestFirst(t *testing.T) {
	a := testutil.NewApp(t)
	adm, cookie := newSuperAdmin(t, a)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	a.DB.Model(&models.User{}).Where("id = ?", adm.ID).Update("created_at", base.Add(10*day))
	insertUser(t, a, "A", "a@x.com", base.Add(1*day))
	insertUser(t, a, "B", "b@x.com", base.Add(5*day))
	insertUser(t, a, "C", "c@x.com", base.Add(3*day))

	r := listUsersReq(t, a, cookie, "")
	got := strings.Join(emailsOf(listOf(t, r)), ",")
	if want := "admin@example.com,b@x.com,c@x.com,a@x.com"; got != want {
		t.Fatalf("order %s, want %s", got, want)
	}
	if r.Body["total"] != float64(4) || r.Body["page"] != float64(1) || r.Body["pageSize"] != float64(50) {
		t.Fatalf("meta: %s", r.Raw)
	}
}

func TestListUsers_CountsAndLastTransaction(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := newSuperAdmin(t, a)
	x := a.CreateUser(t, "x@x.com")
	y := a.CreateUser(t, "y@x.com")
	other := a.CreateUser(t, "other@x.com")
	for _, d := range []int{1, 5, 3} {
		insertTx(t, a, x.ID, time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC))
	}
	insertTx(t, a, other.ID, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	_ = y

	rows := listOf(t, listUsersReq(t, a, cookie, ""))
	xr := rowByEmail(t, rows, "x@x.com")
	if xr["transactionCount"] != float64(3) {
		t.Fatalf("x count: %v", xr["transactionCount"])
	}
	last, err := time.Parse(time.RFC3339, xr["lastTransactionAt"].(string))
	if err != nil || !last.Equal(time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("x last: %v %v", xr["lastTransactionAt"], err)
	}
	yr := rowByEmail(t, rows, "y@x.com")
	if yr["transactionCount"] != float64(0) || yr["lastTransactionAt"] != nil {
		t.Fatalf("y: %v", yr)
	}
}

func TestListUsers_NeverExposesFinancialData(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := newSuperAdmin(t, a)
	x := a.CreateUser(t, "x@x.com")
	insertTx(t, a, x.ID, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))

	r := listUsersReq(t, a, cookie, "")
	raw := string(r.Raw)
	for _, bad := range []string{"balance", "note", "tags", "amount", "secret lunch", "passwordHash", "password"} {
		if strings.Contains(raw, `"`+bad+`"`) || strings.Contains(raw, bad) {
			t.Fatalf("response leaks %q: %s", bad, raw)
		}
	}
	for _, m := range listOf(t, r) {
		if len(m) != 10 {
			t.Fatalf("want 10 keys, got %d: %v", len(m), m)
		}
		for _, k := range []string{"id", "name", "email", "role", "status", "createdAt", "lastLoginAt", "transactionCount", "lastTransactionAt", "inviteExpired"} {
			if _, ok := m[k]; !ok {
				t.Fatalf("missing key %s", k)
			}
		}
	}
}

func TestListUsers_SearchByNameAndEmail(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := newSuperAdmin(t, a)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	insertUser(t, a, "Siti Aminah", "siti@x.com", base)
	insertUser(t, a, "Budi", "budi@mail.com", base)
	insertUser(t, a, "Joko", "joko@x.com", base)
	insertUser(t, a, "50% Off", "promo@x.com", base)

	rows := listOf(t, listUsersReq(t, a, cookie, "?search=sit"))
	if got := emailsOf(rows); len(got) != 1 || got[0] != "siti@x.com" {
		t.Fatalf("sit: %v", got)
	}
	rows = listOf(t, listUsersReq(t, a, cookie, "?search=MAIL.com"))
	if got := emailsOf(rows); len(got) != 1 || got[0] != "budi@mail.com" {
		t.Fatalf("MAIL.com: %v", got)
	}
	r := listUsersReq(t, a, cookie, "?search=zzz")
	if rows := listOf(t, r); len(rows) != 0 || r.Body["total"] != float64(0) {
		t.Fatalf("zzz: %s", r.Raw)
	}
	if rows := listOf(t, listUsersReq(t, a, cookie, "?search=")); len(rows) != 5 {
		t.Fatalf("empty search: %d rows", len(rows))
	}
	rows = listOf(t, listUsersReq(t, a, cookie, "?search=%25"))
	if got := emailsOf(rows); len(got) != 1 || got[0] != "promo@x.com" {
		t.Fatalf("%%: %v", got)
	}
}

func TestListUsers_Pagination(t *testing.T) {
	a := testutil.NewApp(t)
	adm, cookie := newSuperAdmin(t, a)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a.DB.Model(&models.User{}).Where("id = ?", adm.ID).Update("created_at", base.Add(1000*time.Hour))
	for i := 0; i < 50; i++ {
		insertUser(t, a, fmt.Sprintf("U%02d", i), fmt.Sprintf("u%02d@x.com", i), base.Add(time.Duration(i)*time.Hour))
	}

	r := listUsersReq(t, a, cookie, "?page=1")
	rows := listOf(t, r)
	if len(rows) != 50 || rows[0]["email"] != "admin@example.com" || rows[49]["email"] != "u01@x.com" {
		t.Fatalf("page 1: len=%d first=%v last=%v", len(rows), rows[0]["email"], rows[len(rows)-1]["email"])
	}
	r = listUsersReq(t, a, cookie, "?page=2")
	rows = listOf(t, r)
	if len(rows) != 1 || rows[0]["email"] != "u00@x.com" {
		t.Fatalf("page 2: %v", emailsOf(rows))
	}
	if r.Body["total"] != float64(51) || r.Body["page"] != float64(2) || r.Body["pageSize"] != float64(50) {
		t.Fatalf("meta: %s", r.Raw)
	}
	for _, q := range []string{"?page=0", "?page=abc"} {
		r = listUsersReq(t, a, cookie, q)
		if len(listOf(t, r)) != 50 || r.Body["page"] != float64(1) {
			t.Fatalf("%s should be page 1: %s", q, r.Raw)
		}
	}
	r = listUsersReq(t, a, cookie, "?page=3")
	if len(listOf(t, r)) != 0 || r.Body["total"] != float64(51) {
		t.Fatalf("page 3: %s", r.Raw)
	}
}

func TestListUsers_InviteExpired(t *testing.T) {
	a := testutil.NewApp(t)
	t0 := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	testutil.SetNow(t, t0)
	_, cookie := newSuperAdmin(t, a)
	inv := a.CreateUserWith(t, auth.NewUser{Name: "Inv", Email: "inv@x.com", Status: models.UserInvited})
	if _, err := auth.IssueToken(a.DB, inv.ID, models.TokenInvite, 72*time.Hour); err != nil {
		t.Fatal(err)
	}
	a.CreateUserWith(t, auth.NewUser{Name: "Bare", Email: "bare@x.com", Status: models.UserInvited})

	testutil.SetNow(t, t0.Add(71*time.Hour))
	rows := listOf(t, listUsersReq(t, a, cookie, ""))
	if rowByEmail(t, rows, "inv@x.com")["inviteExpired"] != false {
		t.Fatal("inv should not be expired at 71h")
	}
	if rowByEmail(t, rows, "admin@example.com")["inviteExpired"] != false {
		t.Fatal("active user never expired")
	}
	if rowByEmail(t, rows, "bare@x.com")["inviteExpired"] != true {
		t.Fatal("invited without token should be expired")
	}

	testutil.SetNow(t, t0.Add(73*time.Hour))
	rows = listOf(t, listUsersReq(t, a, cookie, ""))
	if rowByEmail(t, rows, "inv@x.com")["inviteExpired"] != true {
		t.Fatal("inv should be expired at 73h")
	}
}

func TestListUsers_NonAdmin404(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := a.NewUser(t, "n@x.com")
	if r := listUsersReq(t, a, cookie, ""); r.Status != 404 {
		t.Fatalf("non-admin: %d", r.Status)
	}
	if r := listUsersReq(t, a, "", ""); r.Status != 401 {
		t.Fatalf("anon: %d", r.Status)
	}
}

func userStatus(t *testing.T, a *testutil.TestApp, id uuid.UUID) string {
	t.Helper()
	var u models.User
	if err := a.DB.First(&u, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	return u.Status
}

func quickAddFixture(t *testing.T, a *testutil.TestApp, userID uuid.UUID, tag string) models.QuickAddLink {
	t.Helper()
	link := models.QuickAddLink{UserID: userID, TokenHash: "h-" + tag, PinHash: "p", Enabled: true}
	if err := a.DB.Create(&link).Error; err != nil {
		t.Fatal(err)
	}
	s := models.QuickAddSession{LinkID: link.ID, TokenHash: "s-" + tag, ExpiresAt: jakarta.Now().Add(30 * time.Minute)}
	if err := a.DB.Create(&s).Error; err != nil {
		t.Fatal(err)
	}
	return link
}

func qaSessionCount(t *testing.T, a *testutil.TestApp, linkID uuid.UUID) int64 {
	t.Helper()
	var n int64
	a.DB.Model(&models.QuickAddSession{}).Where("link_id = ?", linkID).Count(&n)
	return n
}

func act(t *testing.T, a *testutil.TestApp, cookie string, id any, action string) testutil.Response {
	t.Helper()
	return a.Do(t, "POST", fmt.Sprintf("/api/v1/admin/users/%v/%s", id, action), nil, cookie)
}

func TestDeactivate_OK(t *testing.T) {
	a := testutil.NewApp(t)
	adm, aCookie := newSuperAdmin(t, a)
	target, tCookie := a.NewUser(t, "t@x.com")
	bystander := a.CreateUser(t, "by@x.com")
	if r := a.Do(t, "GET", "/api/v1/me", nil, tCookie); r.Status != 200 {
		t.Fatalf("me before: %d", r.Status)
	}
	tLink := quickAddFixture(t, a, target.ID, "target")
	bLink := quickAddFixture(t, a, bystander.ID, "bystander")

	if r := act(t, a, aCookie, target.ID, "deactivate"); r.Status != 204 {
		t.Fatalf("deactivate: %d %s", r.Status, r.Raw)
	}
	if s := userStatus(t, a, target.ID); s != models.UserDeactivated {
		t.Fatalf("status %s", s)
	}
	if r := a.Do(t, "GET", "/api/v1/me", nil, tCookie); r.Status != 401 {
		t.Fatalf("me after: %d", r.Status)
	}
	var sessions int64
	a.DB.Model(&models.Session{}).Where("user_id = ?", target.ID).Count(&sessions)
	if sessions != 0 {
		t.Fatalf("sessions left: %d", sessions)
	}
	r := a.Do(t, "POST", "/api/v1/auth/login", map[string]any{"email": "t@x.com", "password": testutil.Password}, "")
	if r.Status != 403 || r.ErrCode() != "ACCOUNT_DEACTIVATED" {
		t.Fatalf("login: %d %s", r.Status, r.Raw)
	}
	if n := qaSessionCount(t, a, tLink.ID); n != 0 {
		t.Fatalf("target qa sessions: %d", n)
	}
	if n := qaSessionCount(t, a, bLink.ID); n != 1 {
		t.Fatalf("bystander qa sessions: %d", n)
	}
	var link models.QuickAddLink
	if err := a.DB.First(&link, "id = ?", tLink.ID).Error; err != nil || !link.Enabled {
		t.Fatalf("link: %v enabled=%v", err, link.Enabled)
	}
	if n := auditCount(t, a, adm.ID, target.ID, "deactivate_user"); n != 1 {
		t.Fatalf("audit: %d", n)
	}
	if r := a.Do(t, "GET", "/api/v1/me", nil, aCookie); r.Status != 200 {
		t.Fatalf("admin me: %d", r.Status)
	}
}

func TestDeactivate_Self(t *testing.T) {
	a := testutil.NewApp(t)
	adm, cookie := newSuperAdmin(t, a)
	r := act(t, a, cookie, adm.ID, "deactivate")
	if r.Status != 400 || r.ErrCode() != "CANNOT_DEACTIVATE_SELF" {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	if msg := r.Body["error"].(map[string]any)["message"]; msg != "You can't deactivate yourself" {
		t.Fatalf("message %v", msg)
	}
	if s := userStatus(t, a, adm.ID); s != models.UserActive {
		t.Fatalf("status %s", s)
	}
	if n := auditCount(t, a, adm.ID, adm.ID, "deactivate_user"); n != 0 {
		t.Fatalf("audit: %d", n)
	}
}

func TestDeactivate_AlreadyDeactivated(t *testing.T) {
	a := testutil.NewApp(t)
	adm, cookie := newSuperAdmin(t, a)
	target := a.CreateUser(t, "t@x.com")
	for i := 0; i < 2; i++ {
		if r := act(t, a, cookie, target.ID, "deactivate"); r.Status != 204 {
			t.Fatalf("call %d: %d %s", i, r.Status, r.Raw)
		}
	}
	if n := auditCount(t, a, adm.ID, target.ID, "deactivate_user"); n != 1 {
		t.Fatalf("audit: %d", n)
	}
}

func TestDeactivate_UnknownID(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := newSuperAdmin(t, a)
	for _, id := range []any{uuid.New(), "abc"} {
		if r := act(t, a, cookie, id, "deactivate"); r.Status != 404 {
			t.Fatalf("%v: %d", id, r.Status)
		}
	}
}

func TestReactivate_OK(t *testing.T) {
	a := testutil.NewApp(t)
	adm, aCookie := newSuperAdmin(t, a)
	target, tCookie := a.NewUser(t, "t@x.com")
	link := quickAddFixture(t, a, target.ID, "target")
	if r := act(t, a, aCookie, target.ID, "deactivate"); r.Status != 204 {
		t.Fatalf("deactivate: %d", r.Status)
	}
	if r := act(t, a, aCookie, target.ID, "reactivate"); r.Status != 204 {
		t.Fatalf("reactivate: %d %s", r.Status, r.Raw)
	}
	if s := userStatus(t, a, target.ID); s != models.UserActive {
		t.Fatalf("status %s", s)
	}
	if r := a.Do(t, "POST", "/api/v1/auth/login", map[string]any{"email": "t@x.com", "password": testutil.Password}, ""); r.Status != 200 {
		t.Fatalf("login: %d %s", r.Status, r.Raw)
	}
	if r := a.Do(t, "GET", "/api/v1/me", nil, tCookie); r.Status != 401 {
		t.Fatalf("old cookie: %d", r.Status)
	}
	if n := auditCount(t, a, adm.ID, target.ID, "reactivate_user"); n != 1 {
		t.Fatalf("audit: %d", n)
	}
	var got models.QuickAddLink
	if err := a.DB.First(&got, "id = ?", link.ID).Error; err != nil || !got.Enabled {
		t.Fatalf("link: %v enabled=%v", err, got.Enabled)
	}
}

func TestReactivate_NotDeactivated(t *testing.T) {
	a := testutil.NewApp(t)
	adm, cookie := newSuperAdmin(t, a)
	target := a.CreateUser(t, "t@x.com")
	r := act(t, a, cookie, target.ID, "reactivate")
	if r.Status != 400 || r.ErrCode() != "NOT_DEACTIVATED" {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	if msg := r.Body["error"].(map[string]any)["message"]; msg != "This user is not deactivated" {
		t.Fatalf("message %v", msg)
	}
	if n := auditCount(t, a, adm.ID, target.ID, "reactivate_user"); n != 0 {
		t.Fatalf("audit: %d", n)
	}
	if r := act(t, a, cookie, uuid.New(), "reactivate"); r.Status != 404 {
		t.Fatalf("unknown: %d", r.Status)
	}
}

func TestAdminActions_NonAdmin404(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := a.NewUser(t, "n@x.com")
	target := a.CreateUser(t, "t@x.com")
	for _, action := range []string{"deactivate", "reactivate"} {
		if r := act(t, a, cookie, target.ID, action); r.Status != 404 {
			t.Fatalf("%s: %d", action, r.Status)
		}
	}
	if s := userStatus(t, a, target.ID); s != models.UserActive {
		t.Fatalf("status %s", s)
	}
}

func TestAudit_OneRowPerAction(t *testing.T) {
	a := testutil.NewApp(t)
	adm, cookie := newSuperAdmin(t, a)
	target := a.CreateUser(t, "t@x.com")
	for _, action := range []string{"deactivate", "reactivate", "deactivate"} {
		if r := act(t, a, cookie, target.ID, action); r.Status != 204 {
			t.Fatalf("%s: %d %s", action, r.Status, r.Raw)
		}
	}
	var logs []models.AuditLog
	a.DB.Where("target_user_id = ?", target.ID).Order("created_at, id").Find(&logs)
	want := []string{"deactivate_user", "reactivate_user", "deactivate_user"}
	if len(logs) != len(want) {
		t.Fatalf("got %d logs", len(logs))
	}
	for i, l := range logs {
		if l.Action != want[i] || l.AdminID != adm.ID {
			t.Fatalf("log %d: %s admin %s", i, l.Action, l.AdminID)
		}
	}
}
