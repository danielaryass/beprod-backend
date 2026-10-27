package auth_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"beprod/internal/app"
	"beprod/internal/config"
	"beprod/internal/mail"
	"beprod/internal/models"
	"beprod/internal/server"
	"beprod/internal/testutil"
)

func newSignupOpenApp(t *testing.T) *testutil.TestApp {
	t.Helper()
	return testutil.NewApp(t, func(c *config.Config) { c.PublicSignupEnabled = true })
}

func dbUserByEmail(t *testing.T, a *testutil.TestApp, email string) models.User {
	t.Helper()
	var u models.User
	if err := a.DB.Where("email = ?", email).First(&u).Error; err != nil {
		t.Fatalf("user %s not found: %v", email, err)
	}
	return u
}

// failingMailer makes every Send fail.
type failingMailer struct{}

func (failingMailer) Send(mail.Message) error { return errors.New("smtp down") }

func signupBody(email string) map[string]any {
	return map[string]any{"name": "Ann Lee", "email": email, "password": "password1"}
}

const (
	signupPath = "/api/v1/auth/signup"
	verifyPath = "/api/v1/auth/verify-email"
	resendPath = "/api/v1/auth/resend-verification"
	resendOK   = "If this email needs verification, a new link has been sent."
)

var signupT0 = time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)

func countRows(t *testing.T, a *testutil.TestApp, model any) int64 {
	t.Helper()
	var n int64
	if err := a.DB.Model(model).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

// signedUp signs up ann@example.com and returns the emailed token.
func signedUp(t *testing.T, a *testutil.TestApp) string {
	t.Helper()
	if r := a.Do(t, "POST", signupPath, signupBody("ann@example.com"), ""); r.Status != 201 {
		t.Fatalf("signup: %d %s", r.Status, r.Raw)
	}
	m, _ := a.Mailer.Last()
	return testutil.MailToken(t, m)
}

func TestSignup_ClosedByDefault(t *testing.T) {
	a := testutil.NewApp(t)
	r := a.Do(t, "POST", signupPath, signupBody("a@example.com"), "")
	wantStatus(t, r, 403, "SIGNUP_CLOSED")
	if n := countRows(t, a, &models.User{}); n != 0 {
		t.Fatalf("users = %d", n)
	}
	if a.Mailer.Count() != 0 {
		t.Fatal("mail sent")
	}
}

func TestSignup_Closed_CheckedBeforeValidation(t *testing.T) {
	a := testutil.NewApp(t)
	wantStatus(t, a.Do(t, "POST", signupPath, map[string]any{}, ""), 403, "SIGNUP_CLOSED")
}

func TestSignup_OK(t *testing.T) {
	a := newSignupOpenApp(t)
	testutil.SetNow(t, signupT0)
	r := a.Do(t, "POST", signupPath, signupBody("  Ann@Example.COM "), "")
	if r.Status != 201 || r.Body["message"] != "Check your inbox to verify your email." {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	u := dbUserByEmail(t, a, "ann@example.com")
	if u.Status != models.UserPendingVerification || u.Role != models.RoleUser || u.Name != "Ann Lee" ||
		u.PasswordHash == nil || *u.PasswordHash == "password1" {
		t.Fatalf("bad user %+v", u)
	}
	var cats int64
	a.DB.Model(&models.Category{}).Where("user_id = ?", u.ID).Count(&cats)
	if cats != 14 {
		t.Fatalf("categories = %d", cats)
	}
	if a.Mailer.Count() != 1 {
		t.Fatalf("mails = %d", a.Mailer.Count())
	}
	m, _ := a.Mailer.Last()
	if m.To != "ann@example.com" || m.Subject != "Verify your BeProd email" ||
		!strings.Contains(m.Body, "http://app.test/verify-email?token=") || testutil.MailToken(t, m) == "" {
		t.Fatalf("bad mail %+v", m)
	}
	var toks []models.UserToken
	a.DB.Where("user_id = ?", u.ID).Find(&toks)
	if len(toks) != 1 || toks[0].Purpose != models.TokenVerifyEmail || toks[0].UsedAt != nil {
		t.Fatalf("tokens %+v", toks)
	}
	if d := toks[0].ExpiresAt.Sub(signupT0.Add(24 * time.Hour)); d < -time.Minute || d > time.Minute {
		t.Fatalf("expires_at off by %v", d)
	}
}

func TestSignup_DuplicateEmail(t *testing.T) {
	a := newSignupOpenApp(t)
	a.CreateUser(t, "x@example.com")
	if r := a.Do(t, "POST", signupPath, signupBody("Ann@Example.com"), ""); r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	wantStatus(t, a.Do(t, "POST", signupPath, signupBody("ann@EXAMPLE.com"), ""), 409, "EMAIL_TAKEN")
	wantStatus(t, a.Do(t, "POST", signupPath, signupBody("x@example.com"), ""), 409, "EMAIL_TAKEN")
	if n := countRows(t, a, &models.User{}); n != 2 {
		t.Fatalf("users = %d", n)
	}
}

func TestSignup_ValidationFields(t *testing.T) {
	a := newSignupOpenApp(t)
	r := a.Do(t, "POST", signupPath, map[string]any{"name": "   ", "email": "not-an-email", "password": "short"}, "")
	wantStatus(t, r, 400, "VALIDATION_ERROR")
	if r.Field("name") != "Name must be 1–60 characters" || r.Field("email") != "Enter a valid email address" ||
		r.Field("password") != "Password must be 8–72 characters with at least one letter and one digit" {
		t.Fatalf("fields: %s", r.Raw)
	}
	r = a.Do(t, "POST", signupPath, map[string]any{"name": "Ann", "email": "ann@example.com", "password": "12345678"}, "")
	wantStatus(t, r, 400, "VALIDATION_ERROR")
	if r.Field("password") == "" || r.Field("name") != "" {
		t.Fatalf("fields: %s", r.Raw)
	}
	if n := countRows(t, a, &models.User{}); n != 0 {
		t.Fatalf("users = %d", n)
	}
}

func TestSignup_MailFailureStill201(t *testing.T) {
	a := newSignupOpenApp(t)
	a.App = server.New(app.Deps{DB: a.DB, Config: a.Config, Mailer: failingMailer{}})
	if r := a.Do(t, "POST", signupPath, signupBody("ann@example.com"), ""); r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	dbUserByEmail(t, a, "ann@example.com")
	if n := countRows(t, a, &models.UserToken{}); n != 1 {
		t.Fatalf("tokens = %d", n)
	}
}

func TestLogin_BeforeVerify(t *testing.T) {
	a := newSignupOpenApp(t)
	signedUp(t, a)
	r := a.Do(t, "POST", loginPath, loginBody("ann@example.com", "password1"), "")
	wantStatus(t, r, 403, "EMAIL_NOT_VERIFIED")
}

func TestVerifyEmail_OK(t *testing.T) {
	a := newSignupOpenApp(t)
	tok := signedUp(t, a)
	r := a.Do(t, "POST", verifyPath, map[string]any{"token": tok}, "")
	if r.Status != 200 || r.Body["alreadyVerified"] != false || r.Body["message"] != "Email verified. Please log in." {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	u := dbUserByEmail(t, a, "ann@example.com")
	if u.Status != models.UserActive {
		t.Fatalf("status %s", u.Status)
	}
	var tk models.UserToken
	a.DB.Where("user_id = ?", u.ID).First(&tk)
	if tk.UsedAt == nil {
		t.Fatal("token not used")
	}
	if r := a.Do(t, "POST", loginPath, loginBody("ann@example.com", "password1"), ""); r.Status != 200 {
		t.Fatalf("login %d %s", r.Status, r.Raw)
	}
}

func TestVerifyEmail_Twice(t *testing.T) {
	a := newSignupOpenApp(t)
	tok := signedUp(t, a)
	for i := 0; i < 2; i++ {
		r := a.Do(t, "POST", verifyPath, map[string]any{"token": tok}, "")
		if r.Status != 200 {
			t.Fatalf("%d %s", r.Status, r.Raw)
		}
		if i == 1 && (r.Body["alreadyVerified"] != true || r.Body["message"] != "Email already verified. Please log in.") {
			t.Fatalf("second: %s", r.Raw)
		}
	}
}

func TestVerifyEmail_ExpiredAfter24h(t *testing.T) {
	a := newSignupOpenApp(t)
	testutil.SetNow(t, signupT0)
	tok := signedUp(t, a)
	testutil.SetNow(t, signupT0.Add(24*time.Hour+time.Second))
	wantStatus(t, a.Do(t, "POST", verifyPath, map[string]any{"token": tok}, ""), 400, "TOKEN_EXPIRED")
	if u := dbUserByEmail(t, a, "ann@example.com"); u.Status != models.UserPendingVerification {
		t.Fatalf("status %s", u.Status)
	}
}

func TestVerifyEmail_ValidAt23h(t *testing.T) {
	a := newSignupOpenApp(t)
	testutil.SetNow(t, signupT0)
	tok := signedUp(t, a)
	testutil.SetNow(t, signupT0.Add(23*time.Hour))
	if r := a.Do(t, "POST", verifyPath, map[string]any{"token": tok}, ""); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
}

func TestVerifyEmail_UnknownToken(t *testing.T) {
	a := newSignupOpenApp(t)
	wantStatus(t, a.Do(t, "POST", verifyPath, map[string]any{"token": "does-not-exist"}, ""), 400, "TOKEN_INVALID")
	wantStatus(t, a.Do(t, "POST", verifyPath, map[string]any{"token": ""}, ""), 400, "TOKEN_INVALID")
}

func TestResend_TooSoon(t *testing.T) {
	a := newSignupOpenApp(t)
	testutil.SetNow(t, signupT0)
	signedUp(t, a)
	testutil.SetNow(t, signupT0.Add(30*time.Second))
	wantStatus(t, a.Do(t, "POST", resendPath, map[string]any{"email": "ann@example.com"}, ""), 429, "RESEND_TOO_SOON")
	if a.Mailer.Count() != 1 {
		t.Fatalf("mails = %d", a.Mailer.Count())
	}
}

func TestResend_After61sInvalidatesOldToken(t *testing.T) {
	a := newSignupOpenApp(t)
	testutil.SetNow(t, signupT0)
	old := signedUp(t, a)
	testutil.SetNow(t, signupT0.Add(61*time.Second))
	r := a.Do(t, "POST", resendPath, map[string]any{"email": "ann@example.com"}, "")
	if r.Status != 200 || r.Body["message"] != resendOK {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	if a.Mailer.Count() != 2 {
		t.Fatalf("mails = %d", a.Mailer.Count())
	}
	m, _ := a.Mailer.Last()
	newTok := testutil.MailToken(t, m)
	if newTok == old {
		t.Fatal("token not rotated")
	}
	wantStatus(t, a.Do(t, "POST", verifyPath, map[string]any{"token": old}, ""), 400, "TOKEN_EXPIRED")
	if r := a.Do(t, "POST", verifyPath, map[string]any{"token": newTok}, ""); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
}

func TestResend_UnknownEmail(t *testing.T) {
	a := newSignupOpenApp(t)
	a.CreateUser(t, "active@example.com")
	for _, email := range []string{"nobody@example.com", "active@example.com"} {
		r := a.Do(t, "POST", resendPath, map[string]any{"email": email}, "")
		if r.Status != 200 || r.Body["message"] != resendOK {
			t.Fatalf("%s: %d %s", email, r.Status, r.Raw)
		}
	}
	if a.Mailer.Count() != 0 {
		t.Fatalf("mails = %d", a.Mailer.Count())
	}
}
