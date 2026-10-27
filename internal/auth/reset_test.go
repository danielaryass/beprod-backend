package auth_test

import (
	"strings"
	"testing"
	"time"

	"beprod/internal/auth"
	"beprod/internal/models"
	"beprod/internal/testutil"
)

const (
	forgotMsg   = "If this email is registered, a reset link has been sent"
	resetPath   = "/api/v1/auth/reset-password"
	expiredMsg  = "This link has expired. Request a new one."
	weakPwdMsg  = "Password must be 8–72 characters with at least one letter and one digit"
	resetNewPwd = "newpass99"
)

var resetT0 = time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)

func requestReset(t *testing.T, a *testutil.TestApp, email string) testutil.Response {
	t.Helper()
	return a.Do(t, "POST", "/api/v1/auth/forgot-password", map[string]any{"email": email}, "")
}

func login(t *testing.T, a *testutil.TestApp, email, password string) testutil.Response {
	t.Helper()
	return a.Do(t, "POST", "/api/v1/auth/login", map[string]any{"email": email, "password": password}, "")
}

func doReset(t *testing.T, a *testutil.TestApp, token, password string) testutil.Response {
	t.Helper()
	return a.Do(t, "POST", resetPath, map[string]any{"token": token, "password": password}, "")
}

// resetTokenFor requests a reset and returns the token from the last mail.
func resetTokenFor(t *testing.T, a *testutil.TestApp, email string) string {
	t.Helper()
	if r := requestReset(t, a, email); r.Status != 200 {
		t.Fatalf("forgot: %d %s", r.Status, r.Raw)
	}
	m, ok := a.Mailer.Last()
	if !ok {
		t.Fatal("no mail sent")
	}
	return testutil.MailToken(t, m)
}

func countResetTokens(t *testing.T, a *testutil.TestApp, u models.User) []models.UserToken {
	t.Helper()
	var toks []models.UserToken
	if err := a.DB.Where("user_id = ? AND purpose = ?", u.ID, models.TokenResetPassword).Find(&toks).Error; err != nil {
		t.Fatal(err)
	}
	return toks
}

func TestForgotPassword_UnknownEmail(t *testing.T) {
	a := testutil.NewApp(t)
	r := requestReset(t, a, "ghost@example.com")
	if r.Status != 200 || r.Body["message"] != forgotMsg {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
	if a.Mailer.Count() != 0 {
		t.Fatal("mail sent for unknown email")
	}
}

func TestForgotPassword_ActiveUserGetsMail(t *testing.T) {
	a := testutil.NewApp(t)
	testutil.SetNow(t, resetT0)
	u := a.CreateUser(t, "u@example.com")
	r := requestReset(t, a, "  U@Example.com ")
	if r.Status != 200 || r.Body["message"] != forgotMsg {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
	if a.Mailer.Count() != 1 {
		t.Fatalf("mail count %d", a.Mailer.Count())
	}
	m, _ := a.Mailer.Last()
	if m.To != "u@example.com" || m.Subject != "Reset your BeProd password" {
		t.Fatalf("bad mail %+v", m)
	}
	if !strings.Contains(m.Body, "http://app.test/reset-password?token=") || testutil.MailToken(t, m) == "" {
		t.Fatalf("bad body %q", m.Body)
	}
	toks := countResetTokens(t, a, u)
	if len(toks) != 1 {
		t.Fatalf("tokens %d", len(toks))
	}
	if d := toks[0].ExpiresAt.Sub(resetT0.Add(60 * time.Minute)); d < -time.Minute || d > time.Minute {
		t.Fatalf("expires_at %v", toks[0].ExpiresAt)
	}
}

func TestForgotPassword_NonActiveUserNoMail(t *testing.T) {
	for _, st := range []string{models.UserDeactivated, models.UserPendingVerification, models.UserInvited} {
		a := testutil.NewApp(t)
		u := a.CreateUserWith(t, auth.NewUser{Name: "D", Email: "d@example.com", Status: st})
		r := requestReset(t, a, "d@example.com")
		if r.Status != 200 || r.Body["message"] != forgotMsg {
			t.Fatalf("%s: got %d %s", st, r.Status, r.Raw)
		}
		if a.Mailer.Count() != 0 || len(countResetTokens(t, a, u)) != 0 {
			t.Fatalf("%s: mail or token created", st)
		}
	}
}

func TestResetPassword_OK(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "r@example.com")
	if r := a.Do(t, "GET", "/api/v1/me", nil, cookie); r.Status != 200 {
		t.Fatalf("me %d", r.Status)
	}
	tok := resetTokenFor(t, a, "r@example.com")
	r := doReset(t, a, tok, resetNewPwd)
	if r.Status != 200 || r.Body["message"] != "Password updated. Please log in." {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
	if r := a.Do(t, "GET", "/api/v1/me", nil, cookie); r.Status != 401 {
		t.Fatalf("old cookie %d", r.Status)
	}
	if r := login(t, a, "r@example.com", resetNewPwd); r.Status != 200 {
		t.Fatalf("login new %d", r.Status)
	}
	wantStatus(t, login(t, a, "r@example.com", testutil.Password), 401, "INVALID_CREDENTIALS")
	if n := countSessions(t, a.DB, u.ID); n != 1 {
		t.Fatalf("sessions %d, want 1 (from new login)", n)
	}
}

func TestResetPassword_RevokesAllSessions(t *testing.T) {
	a := testutil.NewApp(t)
	u := a.CreateUser(t, "r@example.com")
	c1, c2 := a.LoginAs(t, u.ID), a.LoginAs(t, u.ID)
	if r := doReset(t, a, resetTokenFor(t, a, "r@example.com"), resetNewPwd); r.Status != 200 {
		t.Fatalf("reset %d", r.Status)
	}
	for _, c := range []string{c1, c2} {
		if r := a.Do(t, "GET", "/api/v1/me", nil, c); r.Status != 401 {
			t.Fatalf("me %d", r.Status)
		}
	}
}

func TestResetPassword_SingleUse(t *testing.T) {
	a := testutil.NewApp(t)
	a.CreateUser(t, "r@example.com")
	tok := resetTokenFor(t, a, "r@example.com")
	if r := doReset(t, a, tok, resetNewPwd); r.Status != 200 {
		t.Fatalf("first %d", r.Status)
	}
	r := doReset(t, a, tok, "otherpass1")
	wantStatus(t, r, 400, "TOKEN_EXPIRED")
	if msg := r.Body["error"].(map[string]any)["message"]; msg != expiredMsg {
		t.Fatalf("message %v", msg)
	}
	if r := login(t, a, "r@example.com", resetNewPwd); r.Status != 200 {
		t.Fatalf("first password lost: %d", r.Status)
	}
}

func TestResetPassword_ExpiredAfter60Min(t *testing.T) {
	a := testutil.NewApp(t)
	testutil.SetNow(t, resetT0)
	a.CreateUser(t, "r@example.com")
	tok := resetTokenFor(t, a, "r@example.com")
	testutil.SetNow(t, resetT0.Add(61*time.Minute))
	wantStatus(t, doReset(t, a, tok, resetNewPwd), 400, "TOKEN_EXPIRED")
	if r := login(t, a, "r@example.com", testutil.Password); r.Status != 200 {
		t.Fatalf("old password %d", r.Status)
	}
}

func TestResetPassword_ValidAt59Min(t *testing.T) {
	a := testutil.NewApp(t)
	testutil.SetNow(t, resetT0)
	a.CreateUser(t, "r@example.com")
	tok := resetTokenFor(t, a, "r@example.com")
	testutil.SetNow(t, resetT0.Add(59*time.Minute))
	if r := doReset(t, a, tok, resetNewPwd); r.Status != 200 {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
}

func TestResetPassword_NewRequestInvalidatesOldLink(t *testing.T) {
	a := testutil.NewApp(t)
	a.CreateUser(t, "r@example.com")
	first := resetTokenFor(t, a, "r@example.com")
	second := resetTokenFor(t, a, "r@example.com")
	wantStatus(t, doReset(t, a, first, resetNewPwd), 400, "TOKEN_EXPIRED")
	if r := doReset(t, a, second, resetNewPwd); r.Status != 200 {
		t.Fatalf("second %d", r.Status)
	}
}

func TestResetPassword_UnknownToken(t *testing.T) {
	a := testutil.NewApp(t)
	wantStatus(t, doReset(t, a, "nope", resetNewPwd), 400, "TOKEN_EXPIRED")
}

func TestResetPassword_WeakPassword(t *testing.T) {
	a := testutil.NewApp(t)
	a.CreateUser(t, "r@example.com")
	tok := resetTokenFor(t, a, "r@example.com")
	r := doReset(t, a, tok, "short")
	wantStatus(t, r, 400, "VALIDATION_ERROR")
	if r.Field("password") != weakPwdMsg {
		t.Fatalf("field %q", r.Field("password"))
	}
	if r := doReset(t, a, tok, resetNewPwd); r.Status != 200 {
		t.Fatalf("token burned: %d", r.Status)
	}
}

func TestResetPassword_GoogleOnlyUserCanSetPassword(t *testing.T) {
	a := testutil.NewApp(t)
	sub := "g-sub-1"
	a.CreateUserWith(t, auth.NewUser{Name: "G", Email: "g@example.com", GoogleSubject: &sub, Status: models.UserActive})
	if r := login(t, a, "g@example.com", "anything1"); r.Status != 401 {
		t.Fatalf("login %d", r.Status)
	}
	tok := resetTokenFor(t, a, "g@example.com")
	if r := doReset(t, a, tok, resetNewPwd); r.Status != 200 {
		t.Fatalf("reset %d", r.Status)
	}
	if r := login(t, a, "g@example.com", resetNewPwd); r.Status != 200 {
		t.Fatalf("login new %d", r.Status)
	}
}

func TestResetPassword_DeactivatedSinceRequest(t *testing.T) {
	a := testutil.NewApp(t)
	u := a.CreateUser(t, "r@example.com")
	tok := resetTokenFor(t, a, "r@example.com")
	if err := a.DB.Model(&models.User{}).Where("id = ?", u.ID).Update("status", models.UserDeactivated).Error; err != nil {
		t.Fatal(err)
	}
	wantStatus(t, doReset(t, a, tok, resetNewPwd), 400, "TOKEN_EXPIRED")
}
