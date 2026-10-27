package admin_test

import (
	"testing"
	"time"

	"beprod/internal/admin"
	"beprod/internal/auth"
	"beprod/internal/models"
	"beprod/internal/testutil"

	"github.com/google/uuid"
)

const (
	adminUsersPath = "/api/v1/admin/users"
	acceptPath     = "/api/v1/auth/accept-invite"
)

var adminT0 = time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)

// newSuperAdmin returns a logged-in super admin and the cookie.
func newSuperAdmin(t *testing.T, a *testutil.TestApp) (models.User, string) {
	t.Helper()
	u, cookie := a.NewUser(t, "admin@example.com")
	if err := a.DB.Model(&models.User{}).Where("id = ?", u.ID).Update("role", models.RoleSuperAdmin).Error; err != nil {
		t.Fatal(err)
	}
	u.Role = models.RoleSuperAdmin
	return u, cookie
}

func countAudits(t *testing.T, a *testutil.TestApp, action string) int64 {
	t.Helper()
	var n int64
	if err := a.DB.Model(&models.AuditLog{}).Where("action = ?", action).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func invite(t *testing.T, a *testutil.TestApp, cookie, name, email string) testutil.Response {
	t.Helper()
	return a.Do(t, "POST", adminUsersPath, map[string]any{"name": name, "email": email}, cookie)
}

func TestAdminUsers_NonAdminGets404(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := a.NewUser(t, "n@example.com")
	r := invite(t, a, cookie, "X", "x@example.com")
	if r.Status != 404 || r.ErrCode() != "NOT_FOUND" {
		t.Fatalf("create: %d %s", r.Status, r.Raw)
	}
	if n := countUsers(t, a); n != 1 {
		t.Fatalf("users = %d", n)
	}
	if r := invite(t, a, "", "X", "x@example.com"); r.Status != 401 {
		t.Fatalf("no cookie: %d", r.Status)
	}
}

func loginStatus(t *testing.T, a *testutil.TestApp, email, password string) int {
	t.Helper()
	return a.Do(t, "POST", "/api/v1/auth/login", map[string]any{"email": email, "password": password}, "").Status
}

func TestCreateUser_OK(t *testing.T) {
	a := testutil.NewApp(t)
	adm, cookie := newSuperAdmin(t, a)
	r := invite(t, a, cookie, "  Budi  ", "  Budi@Example.COM ")
	if r.Status != 201 {
		t.Fatalf("status %d %s", r.Status, r.Raw)
	}
	user := r.Body["user"].(map[string]any)
	want := map[string]any{"email": "budi@example.com", "name": "Budi", "status": "active", "role": "user", "transactionCount": float64(0), "inviteExpired": false}
	for k, v := range want {
		if user[k] != v {
			t.Fatalf("%s = %v, want %v", k, user[k], v)
		}
	}
	if _, ok := user["passwordHash"]; ok {
		t.Fatal("passwordHash leaked")
	}
	u := userByEmail(t, a, "budi@example.com")
	if u.CreatedBy == nil || *u.CreatedBy != adm.ID || u.PasswordHash == nil {
		t.Fatalf("user = %+v", u)
	}
	var cats int64
	a.DB.Model(&models.Category{}).Where("user_id = ?", u.ID).Count(&cats)
	if cats != 14 {
		t.Fatalf("categories = %d", cats)
	}
	var toks int64
	a.DB.Model(&models.UserToken{}).Where("user_id = ?", u.ID).Count(&toks)
	if toks != 0 || a.Mailer.Count() != 0 {
		t.Fatalf("tokens = %d mails = %d, want none", toks, a.Mailer.Count())
	}
	if s := loginStatus(t, a, "budi@example.com", admin.DefaultPassword); s != 200 {
		t.Fatalf("login with default password: %d", s)
	}
	var audits []models.AuditLog
	a.DB.Where("action = ?", models.AuditCreateUser).Find(&audits)
	if len(audits) != 1 || audits[0].AdminID != adm.ID || audits[0].TargetUserID != u.ID {
		t.Fatalf("audits = %+v", audits)
	}
}

func TestCreateUser_Duplicate(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := newSuperAdmin(t, a)
	a.CreateUser(t, "taken@example.com")
	if r := invite(t, a, cookie, "A", "a@example.com"); r.Status != 201 {
		t.Fatalf("first: %d", r.Status)
	}
	mails, audits := a.Mailer.Count(), countAudits(t, a, models.AuditCreateUser)
	for _, email := range []string{"A@Example.com", "taken@example.com"} {
		r := invite(t, a, cookie, "A", email)
		if r.Status != 409 || r.ErrCode() != "EMAIL_TAKEN" {
			t.Fatalf("%s: %d %s", email, r.Status, r.Raw)
		}
		if msg, _ := r.Body["error"].(map[string]any)["message"].(string); msg != "A user with this email already exists" {
			t.Fatalf("message = %q", msg)
		}
	}
	if a.Mailer.Count() != mails || countAudits(t, a, models.AuditCreateUser) != audits {
		t.Fatal("failed create left mail or audit")
	}
}

func TestCreateUser_ValidationFields(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := newSuperAdmin(t, a)
	r := invite(t, a, cookie, "", "bad")
	if r.Status != 400 || r.Field("name") != "Name must be 1–60 characters" || r.Field("email") != "Enter a valid email address" {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	if n := countUsers(t, a); n != 1 {
		t.Fatalf("users = %d", n)
	}
}

func TestResetPassword_OK(t *testing.T) {
	a := testutil.NewApp(t)
	adm, cookie := newSuperAdmin(t, a)
	target, oldCookie := a.NewUser(t, "r@example.com")
	if r := act(t, a, cookie, target.ID, "reset-password"); r.Status != 204 {
		t.Fatalf("reset: %d %s", r.Status, r.Raw)
	}
	if s := loginStatus(t, a, "r@example.com", testutil.Password); s != 401 {
		t.Fatalf("old password still works: %d", s)
	}
	if s := loginStatus(t, a, "r@example.com", admin.DefaultPassword); s != 200 {
		t.Fatalf("default password: %d", s)
	}
	if r := a.Do(t, "GET", "/api/v1/me", nil, oldCookie); r.Status != 401 {
		t.Fatalf("old session survived: %d", r.Status)
	}
	var audits []models.AuditLog
	a.DB.Where("action = ?", models.AuditResetPassword).Find(&audits)
	if len(audits) != 1 || audits[0].AdminID != adm.ID || audits[0].TargetUserID != target.ID {
		t.Fatalf("audits = %+v", audits)
	}
}

func TestResetPassword_ActivatesInvitedUser(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := newSuperAdmin(t, a)
	u := a.CreateUserWith(t, auth.NewUser{Name: "I", Email: "i@example.com", Status: models.UserInvited})
	if r := act(t, a, cookie, u.ID, "reset-password"); r.Status != 204 {
		t.Fatalf("reset: %d %s", r.Status, r.Raw)
	}
	if s := userStatus(t, a, u.ID); s != models.UserActive {
		t.Fatalf("status %s", s)
	}
	if s := loginStatus(t, a, "i@example.com", admin.DefaultPassword); s != 200 {
		t.Fatalf("login: %d", s)
	}
}

func TestResetPassword_KeepsDeactivated(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := newSuperAdmin(t, a)
	u := a.CreateUserWith(t, auth.NewUser{Name: "D", Email: "d@example.com", Status: models.UserDeactivated})
	if r := act(t, a, cookie, u.ID, "reset-password"); r.Status != 204 {
		t.Fatalf("reset: %d %s", r.Status, r.Raw)
	}
	if s := userStatus(t, a, u.ID); s != models.UserDeactivated {
		t.Fatalf("status %s", s)
	}
}

func TestResetPassword_SelfUnknownNonAdmin(t *testing.T) {
	a := testutil.NewApp(t)
	adm, cookie := newSuperAdmin(t, a)
	if r := act(t, a, cookie, adm.ID, "reset-password"); r.Status != 400 || r.ErrCode() != "CANNOT_RESET_SELF" {
		t.Fatalf("self: %d %s", r.Status, r.Raw)
	}
	if r := act(t, a, cookie, uuid.New(), "reset-password"); r.Status != 404 {
		t.Fatalf("unknown: %d", r.Status)
	}
	other, otherCookie := a.NewUser(t, "n@example.com")
	if r := act(t, a, otherCookie, other.ID, "reset-password"); r.Status != 404 {
		t.Fatalf("non-admin: %d", r.Status)
	}
}
