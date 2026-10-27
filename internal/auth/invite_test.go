package auth_test

import (
	"testing"
	"time"

	"beprod/internal/auth"
	"beprod/internal/models"
	"beprod/internal/testutil"
)

const (
	acceptInvitePath = "/api/v1/auth/accept-invite"
	inviteExpiredMsg = "This link has expired. Ask the administrator for a new invite."
	invitePwd        = "newpass99"
)

var inviteT0 = time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)

func invitedUser(t *testing.T, a *testutil.TestApp, email string) (models.User, string) {
	t.Helper()
	u := a.CreateUserWith(t, auth.NewUser{Name: "Inv", Email: email, Status: models.UserInvited})
	tok, err := auth.IssueToken(a.DB, u.ID, models.TokenInvite, 72*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return u, tok
}

func acceptInvite(t *testing.T, a *testutil.TestApp, tok, pw string) testutil.Response {
	t.Helper()
	return a.Do(t, "POST", acceptInvitePath, map[string]any{"token": tok, "password": pw}, "")
}

func TestAcceptInvite_OK(t *testing.T) {
	a := testutil.NewApp(t)
	u, tok := invitedUser(t, a, "inv@example.com")
	r := acceptInvite(t, a, tok, invitePwd)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	user := r.Body["user"].(map[string]any)
	if user["status"] != "active" || user["hasPassword"] != true {
		t.Fatalf("user = %v", user)
	}
	cookie := r.Cookie("beprod_session")
	if cookie == "" {
		t.Fatal("no session cookie")
	}
	if me := a.Do(t, "GET", "/api/v1/me", nil, "beprod_session="+cookie); me.Status != 200 {
		t.Fatalf("me: %d", me.Status)
	}
	var got models.User
	a.DB.First(&got, "id = ?", u.ID)
	if got.Status != models.UserActive || got.PasswordHash == nil || got.LastLoginAt == nil {
		t.Fatalf("db user = %+v", got)
	}
	var tk models.UserToken
	a.DB.Where("user_id = ?", u.ID).First(&tk)
	if tk.UsedAt == nil {
		t.Fatal("token not used")
	}
	l := a.Do(t, "POST", "/api/v1/auth/login", map[string]any{"email": "inv@example.com", "password": invitePwd}, "")
	if l.Status != 200 {
		t.Fatalf("login: %d %s", l.Status, l.Raw)
	}
}

func TestAcceptInvite_ExpiredAfter72h(t *testing.T) {
	a := testutil.NewApp(t)
	testutil.SetNow(t, inviteT0)
	u, tok := invitedUser(t, a, "inv@example.com")
	testutil.SetNow(t, inviteT0.Add(72*time.Hour+time.Second))
	r := acceptInvite(t, a, tok, invitePwd)
	if r.Status != 400 || r.ErrCode() != "TOKEN_EXPIRED" {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	if msg, _ := r.Body["error"].(map[string]any)["message"].(string); msg != inviteExpiredMsg {
		t.Fatalf("message = %q", msg)
	}
	var got models.User
	a.DB.First(&got, "id = ?", u.ID)
	if got.Status != models.UserInvited {
		t.Fatalf("status = %s", got.Status)
	}
}

func TestAcceptInvite_ValidAt71h(t *testing.T) {
	a := testutil.NewApp(t)
	testutil.SetNow(t, inviteT0)
	_, tok := invitedUser(t, a, "inv@example.com")
	testutil.SetNow(t, inviteT0.Add(71*time.Hour))
	if r := acceptInvite(t, a, tok, invitePwd); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
}

func TestAcceptInvite_UnknownToken(t *testing.T) {
	a := testutil.NewApp(t)
	r := acceptInvite(t, a, "nope", invitePwd)
	if r.Status != 400 || r.ErrCode() != "TOKEN_EXPIRED" {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
}

func TestAcceptInvite_UsedTwice(t *testing.T) {
	a := testutil.NewApp(t)
	_, tok := invitedUser(t, a, "inv@example.com")
	if r := acceptInvite(t, a, tok, invitePwd); r.Status != 200 {
		t.Fatalf("first: %d", r.Status)
	}
	r := acceptInvite(t, a, tok, invitePwd)
	if r.Status != 400 || r.ErrCode() != "TOKEN_EXPIRED" {
		t.Fatalf("second: %d %s", r.Status, r.Raw)
	}
}

func TestAcceptInvite_WeakPassword(t *testing.T) {
	a := testutil.NewApp(t)
	_, tok := invitedUser(t, a, "inv@example.com")
	r := acceptInvite(t, a, tok, "short")
	if r.Status != 400 || r.ErrCode() != "VALIDATION_ERROR" || r.Field("password") != weakPwdMsg {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	if r.Cookie("beprod_session") != "" {
		t.Fatal("cookie set on 400")
	}
	if r := acceptInvite(t, a, tok, invitePwd); r.Status != 200 {
		t.Fatalf("retry: %d %s", r.Status, r.Raw)
	}
}

func TestAcceptInvite_Deactivated(t *testing.T) {
	a := testutil.NewApp(t)
	u, tok := invitedUser(t, a, "inv@example.com")
	if err := a.DB.Model(&models.User{}).Where("id = ?", u.ID).Update("status", models.UserDeactivated).Error; err != nil {
		t.Fatal(err)
	}
	r := acceptInvite(t, a, tok, invitePwd)
	if r.Status != 403 || r.ErrCode() != "ACCOUNT_DEACTIVATED" || r.Cookie("beprod_session") != "" {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
}

func TestAcceptInvite_WrongPurposeToken(t *testing.T) {
	a := testutil.NewApp(t)
	u, _ := invitedUser(t, a, "inv@example.com")
	for _, purpose := range []string{models.TokenVerifyEmail, models.TokenResetPassword} {
		tok, err := auth.IssueToken(a.DB, u.ID, purpose, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		r := acceptInvite(t, a, tok, invitePwd)
		if r.Status != 400 || r.ErrCode() != "TOKEN_EXPIRED" {
			t.Fatalf("%s: %d %s", purpose, r.Status, r.Raw)
		}
	}
}
