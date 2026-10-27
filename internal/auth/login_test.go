package auth_test

import (
	"strings"
	"testing"
	"time"

	"beprod/internal/auth"
	"beprod/internal/models"
	"beprod/internal/secure"
	"beprod/internal/testutil"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var t0 = time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)

const loginPath = "/api/v1/auth/login"
const logoutPath = "/api/v1/auth/logout"
const meMsgBad = "Incorrect email or password"

func loginBody(email, pw string) map[string]string {
	return map[string]string{"email": email, "password": pw}
}

func countFailures(t *testing.T, g *gorm.DB, email string) int64 {
	t.Helper()
	var n int64
	if err := g.Model(&models.LoginFailure{}).Where("email = ?", email).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func countSessions(t *testing.T, g *gorm.DB, userID uuid.UUID) int64 {
	t.Helper()
	var n int64
	if err := g.Model(&models.Session{}).Where("user_id = ?", userID).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

// addFailures inserts n LoginFailure rows for email created at `at`.
func addFailures(t *testing.T, g *gorm.DB, email string, n int, at time.Time) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := g.Create(&models.LoginFailure{Email: email, CreatedAt: at}).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func userWithStatus(t *testing.T, a *testutil.TestApp, email, status string) models.User {
	t.Helper()
	h, err := secure.HashPassword(testutil.Password)
	if err != nil {
		t.Fatal(err)
	}
	return a.CreateUserWith(t, auth.NewUser{Name: "S", Email: email, PasswordHash: &h, Status: status})
}

func wantStatus(t *testing.T, r testutil.Response, status int, code string) {
	t.Helper()
	if r.Status != status || r.ErrCode() != code {
		t.Fatalf("want %d %s, got %d %s (%s)", status, code, r.Status, r.ErrCode(), r.Raw)
	}
}

func TestLogin_Success(t *testing.T) {
	a := testutil.NewApp(t)
	testutil.SetNow(t, t0)
	u := a.CreateUser(t, "user@example.com")
	r := a.Do(t, "POST", loginPath, loginBody("user@example.com", testutil.Password), "")
	if r.Status != 200 {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
	user, _ := r.Body["user"].(map[string]any)
	if user["email"] != "user@example.com" || user["id"] != u.ID.String() || user["hasPassword"] != true {
		t.Errorf("bad user: %v", user)
	}
	if _, ok := user["passwordHash"]; ok {
		t.Error("passwordHash leaked")
	}
	tok := r.Cookie("beprod_session")
	if tok == "" {
		t.Fatal("no cookie")
	}
	var got models.User
	if err := a.DB.First(&got, "id = ?", u.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.LastLoginAt == nil || !got.LastLoginAt.Equal(t0) {
		t.Errorf("last_login_at = %v", got.LastLoginAt)
	}
	if n := countSessions(t, a.DB, u.ID); n != 1 {
		t.Errorf("sessions = %d", n)
	}
	if m := a.Do(t, "GET", "/api/v1/me", nil, "beprod_session="+tok); m.Status != 200 {
		t.Errorf("me = %d", m.Status)
	}
}

func TestLogin_EmailCaseInsensitiveAndTrimmed(t *testing.T) {
	a := testutil.NewApp(t)
	a.CreateUser(t, "user@example.com")
	r := a.Do(t, "POST", loginPath, loginBody("  USER@Example.COM ", testutil.Password), "")
	if r.Status != 200 {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	a := testutil.NewApp(t)
	u := a.CreateUser(t, "user@example.com")
	r := a.Do(t, "POST", loginPath, loginBody("user@example.com", "wrong-pass1"), "")
	wantStatus(t, r, 401, "INVALID_CREDENTIALS")
	if m := r.Body["error"].(map[string]any)["message"]; m != meMsgBad {
		t.Errorf("message = %v", m)
	}
	if r.Cookie("beprod_session") != "" {
		t.Error("cookie set")
	}
	if n := countFailures(t, a.DB, "user@example.com"); n != 1 {
		t.Errorf("failures = %d", n)
	}
	if n := countSessions(t, a.DB, u.ID); n != 0 {
		t.Errorf("sessions = %d", n)
	}
}

func TestLogin_UnknownEmail(t *testing.T) {
	a := testutil.NewApp(t)
	r := a.Do(t, "POST", loginPath, loginBody("ghost@example.com", "whatever1"), "")
	wantStatus(t, r, 401, "INVALID_CREDENTIALS")
	if m := r.Body["error"].(map[string]any)["message"]; m != meMsgBad {
		t.Errorf("message = %v", m)
	}
	if n := countFailures(t, a.DB, "ghost@example.com"); n != 1 {
		t.Errorf("failures = %d", n)
	}
}

func TestLogin_GoogleOnlyUserWithoutPassword(t *testing.T) {
	a := testutil.NewApp(t)
	a.CreateUserWith(t, auth.NewUser{Name: "G", Email: "g@example.com", Status: models.UserActive})
	r := a.Do(t, "POST", loginPath, loginBody("g@example.com", "anything1"), "")
	wantStatus(t, r, 401, "INVALID_CREDENTIALS")
}

func TestLogin_ValidationErrors(t *testing.T) {
	a := testutil.NewApp(t)
	r := a.Do(t, "POST", loginPath, loginBody("", "x"), "")
	wantStatus(t, r, 400, "VALIDATION_ERROR")
	if r.Field("email") != "Enter a valid email address" {
		t.Errorf("email field = %q", r.Field("email"))
	}
	r = a.Do(t, "POST", loginPath, loginBody("a@b.com", ""), "")
	wantStatus(t, r, 400, "VALIDATION_ERROR")
	if r.Field("password") != "Password is required" {
		t.Errorf("password field = %q", r.Field("password"))
	}
	r = a.Do(t, "POST", loginPath, loginBody("not-an-email", "x"), "")
	wantStatus(t, r, 400, "VALIDATION_ERROR")
	if r.Field("email") == "" {
		t.Error("email field empty")
	}
	var n int64
	a.DB.Model(&models.LoginFailure{}).Count(&n)
	if n != 0 {
		t.Errorf("failures = %d", n)
	}
}

func TestLogin_InvalidJSONBody(t *testing.T) {
	a := testutil.NewApp(t)
	r := a.Do(t, "POST", loginPath, "just a string", "")
	wantStatus(t, r, 400, "INVALID_BODY")
	var n int64
	a.DB.Model(&models.LoginFailure{}).Count(&n)
	if n != 0 {
		t.Errorf("failures = %d", n)
	}
}

func TestLogin_MissingCSRFHeader403(t *testing.T) {
	a := testutil.NewApp(t)
	r := a.DoH(t, "POST", loginPath, loginBody("a@b.com", "x"), map[string]string{"X-BeProd-CSRF": ""})
	wantStatus(t, r, 403, "CSRF_REQUIRED")
}

func TestLogin_RateLimit_11thAttemptBlockedEvenWithCorrectPassword(t *testing.T) {
	a := testutil.NewApp(t)
	testutil.SetNow(t, t0)
	u := a.CreateUser(t, "user@example.com")
	for i := 0; i < 10; i++ {
		wantStatus(t, a.Do(t, "POST", loginPath, loginBody("user@example.com", "wrong-pass1"), ""), 401, "INVALID_CREDENTIALS")
	}
	if n := countFailures(t, a.DB, "user@example.com"); n != 10 {
		t.Fatalf("failures = %d", n)
	}
	r := a.Do(t, "POST", loginPath, loginBody("user@example.com", testutil.Password), "")
	wantStatus(t, r, 429, "TOO_MANY_ATTEMPTS")
	if m := r.Body["error"].(map[string]any)["message"]; m != "Too many attempts. Try again in 15 minutes." {
		t.Errorf("message = %v", m)
	}
	if r.Cookie("beprod_session") != "" {
		t.Error("cookie set")
	}
	if n := countSessions(t, a.DB, u.ID); n != 0 {
		t.Errorf("sessions = %d", n)
	}
	if n := countFailures(t, a.DB, "user@example.com"); n != 10 {
		t.Errorf("failures after 429 = %d", n)
	}
}

func TestLogin_RateLimit_UnknownEmailAlsoLimited(t *testing.T) {
	a := testutil.NewApp(t)
	testutil.SetNow(t, t0)
	for i := 0; i < 10; i++ {
		wantStatus(t, a.Do(t, "POST", loginPath, loginBody("ghost@example.com", "whatever1"), ""), 401, "INVALID_CREDENTIALS")
	}
	wantStatus(t, a.Do(t, "POST", loginPath, loginBody("ghost@example.com", "whatever1"), ""), 429, "TOO_MANY_ATTEMPTS")
}

func TestLogin_RateLimit_IsPerEmail(t *testing.T) {
	a := testutil.NewApp(t)
	testutil.SetNow(t, t0)
	a.CreateUser(t, "a@example.com")
	a.CreateUser(t, "b@example.com")
	addFailures(t, a.DB, "a@example.com", 10, t0)
	if r := a.Do(t, "POST", loginPath, loginBody("b@example.com", testutil.Password), ""); r.Status != 200 {
		t.Errorf("B got %d", r.Status)
	}
	wantStatus(t, a.Do(t, "POST", loginPath, loginBody("a@example.com", testutil.Password), ""), 429, "TOO_MANY_ATTEMPTS")
}

func TestLogin_NineFailuresStillAllowed(t *testing.T) {
	a := testutil.NewApp(t)
	testutil.SetNow(t, t0)
	a.CreateUser(t, "user@example.com")
	addFailures(t, a.DB, "user@example.com", 9, t0)
	if r := a.Do(t, "POST", loginPath, loginBody("user@example.com", testutil.Password), ""); r.Status != 200 {
		t.Errorf("got %d", r.Status)
	}
}

func TestLogin_FailuresOlderThan15MinutesIgnored(t *testing.T) {
	a := testutil.NewApp(t)
	a.CreateUser(t, "user@example.com")
	addFailures(t, a.DB, "user@example.com", 10, t0.Add(-16*time.Minute))
	testutil.SetNow(t, t0)
	if r := a.Do(t, "POST", loginPath, loginBody("user@example.com", testutil.Password), ""); r.Status != 200 {
		t.Errorf("got %d", r.Status)
	}
}

func TestLogin_FailuresWindowSlides(t *testing.T) {
	a := testutil.NewApp(t)
	a.CreateUser(t, "user@example.com")
	addFailures(t, a.DB, "user@example.com", 10, t0.Add(-14*time.Minute))
	testutil.SetNow(t, t0)
	wantStatus(t, a.Do(t, "POST", loginPath, loginBody("user@example.com", testutil.Password), ""), 429, "TOO_MANY_ATTEMPTS")
	testutil.SetNow(t, t0.Add(2*time.Minute))
	if r := a.Do(t, "POST", loginPath, loginBody("user@example.com", testutil.Password), ""); r.Status != 200 {
		t.Errorf("got %d", r.Status)
	}
}

func TestLogin_FailureExactly15MinutesOldIgnored(t *testing.T) {
	a := testutil.NewApp(t)
	a.CreateUser(t, "user@example.com")
	addFailures(t, a.DB, "user@example.com", 10, t0.Add(-15*time.Minute))
	testutil.SetNow(t, t0)
	if r := a.Do(t, "POST", loginPath, loginBody("user@example.com", testutil.Password), ""); r.Status != 200 {
		t.Errorf("got %d", r.Status)
	}
}

func TestLogin_SuccessClearsFailures(t *testing.T) {
	a := testutil.NewApp(t)
	testutil.SetNow(t, t0)
	a.CreateUser(t, "user@example.com")
	for i := 0; i < 3; i++ {
		wantStatus(t, a.Do(t, "POST", loginPath, loginBody("user@example.com", "wrong-pass1"), ""), 401, "INVALID_CREDENTIALS")
	}
	addFailures(t, a.DB, "other@example.com", 2, t0)
	if r := a.Do(t, "POST", loginPath, loginBody("user@example.com", testutil.Password), ""); r.Status != 200 {
		t.Fatalf("got %d", r.Status)
	}
	if n := countFailures(t, a.DB, "user@example.com"); n != 0 {
		t.Errorf("failures = %d", n)
	}
	if n := countFailures(t, a.DB, "other@example.com"); n != 2 {
		t.Errorf("other failures = %d", n)
	}
}

func TestLogin_PendingVerification403(t *testing.T) {
	a := testutil.NewApp(t)
	u := userWithStatus(t, a, "p@example.com", models.UserPendingVerification)
	r := a.Do(t, "POST", loginPath, loginBody("p@example.com", testutil.Password), "")
	wantStatus(t, r, 403, "EMAIL_NOT_VERIFIED")
	if m := r.Body["error"].(map[string]any)["message"]; m != "Please verify your email" {
		t.Errorf("message = %v", m)
	}
	if n := countSessions(t, a.DB, u.ID); n != 0 {
		t.Errorf("sessions = %d", n)
	}
	if r.Cookie("beprod_session") != "" {
		t.Error("cookie set")
	}
	if n := countFailures(t, a.DB, "p@example.com"); n != 0 {
		t.Errorf("failures = %d", n)
	}
	var got models.User
	a.DB.First(&got, "id = ?", u.ID)
	if got.LastLoginAt != nil {
		t.Error("last_login_at set")
	}
}

func TestLogin_Invited403(t *testing.T) {
	a := testutil.NewApp(t)
	userWithStatus(t, a, "i@example.com", models.UserInvited)
	r := a.Do(t, "POST", loginPath, loginBody("i@example.com", testutil.Password), "")
	wantStatus(t, r, 403, "INVITE_PENDING")
	if m := r.Body["error"].(map[string]any)["message"]; m != "Please use your invite link to set a password" {
		t.Errorf("message = %v", m)
	}
}

func TestLogin_Deactivated403(t *testing.T) {
	a := testutil.NewApp(t)
	u := userWithStatus(t, a, "d@example.com", models.UserDeactivated)
	r := a.Do(t, "POST", loginPath, loginBody("d@example.com", testutil.Password), "")
	wantStatus(t, r, 403, "ACCOUNT_DEACTIVATED")
	if m := r.Body["error"].(map[string]any)["message"]; m != "Your account has been deactivated. Contact the administrator." {
		t.Errorf("message = %v", m)
	}
	if n := countSessions(t, a.DB, u.ID); n != 0 {
		t.Errorf("sessions = %d", n)
	}
}

func TestLogin_PendingWithWrongPasswordIs401(t *testing.T) {
	a := testutil.NewApp(t)
	userWithStatus(t, a, "p@example.com", models.UserPendingVerification)
	userWithStatus(t, a, "d@example.com", models.UserDeactivated)
	wantStatus(t, a.Do(t, "POST", loginPath, loginBody("p@example.com", "wrong-pass1"), ""), 401, "INVALID_CREDENTIALS")
	wantStatus(t, a.Do(t, "POST", loginPath, loginBody("d@example.com", "wrong-pass1"), ""), 401, "INVALID_CREDENTIALS")
}

func TestLogout_Success(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u@example.com")
	r := a.Do(t, "POST", logoutPath, nil, cookie)
	if r.Status != 204 || len(r.Raw) != 0 {
		t.Fatalf("got %d %q", r.Status, r.Raw)
	}
	if n := countSessions(t, a.DB, u.ID); n != 0 {
		t.Errorf("sessions = %d", n)
	}
	if !strings.Contains(strings.Join(r.Header.Values("Set-Cookie"), ";"), "beprod_session") {
		t.Error("no Set-Cookie")
	}
	if r.Cookie("beprod_session") != "" {
		t.Error("cookie not cleared")
	}
	if m := a.Do(t, "GET", "/api/v1/me", nil, cookie); m.Status != 401 {
		t.Errorf("me = %d", m.Status)
	}
}

func TestLogout_NoCookie401(t *testing.T) {
	a := testutil.NewApp(t)
	wantStatus(t, a.Do(t, "POST", logoutPath, nil, ""), 401, "UNAUTHENTICATED")
}

func TestLogout_OnlyCurrentSession(t *testing.T) {
	a := testutil.NewApp(t)
	u := a.CreateUser(t, "u@example.com")
	c1, c2 := a.LoginAs(t, u.ID), a.LoginAs(t, u.ID)
	if r := a.Do(t, "POST", logoutPath, nil, c1); r.Status != 204 {
		t.Fatalf("got %d", r.Status)
	}
	if n := countSessions(t, a.DB, u.ID); n != 1 {
		t.Errorf("sessions = %d", n)
	}
	if m := a.Do(t, "GET", "/api/v1/me", nil, c2); m.Status != 200 {
		t.Errorf("me = %d", m.Status)
	}
}

func TestLogin_ThenLogoutThenLogin(t *testing.T) {
	a := testutil.NewApp(t)
	a.CreateUser(t, "u@example.com")
	r1 := a.Do(t, "POST", loginPath, loginBody("u@example.com", testutil.Password), "")
	tok1 := r1.Cookie("beprod_session")
	if r1.Status != 200 || tok1 == "" {
		t.Fatalf("login1 %d", r1.Status)
	}
	if r := a.Do(t, "POST", logoutPath, nil, "beprod_session="+tok1); r.Status != 204 {
		t.Fatalf("logout %d", r.Status)
	}
	r2 := a.Do(t, "POST", loginPath, loginBody("u@example.com", testutil.Password), "")
	if r2.Status != 200 || r2.Cookie("beprod_session") == "" || r2.Cookie("beprod_session") == tok1 {
		t.Errorf("login2 %d, cookie same or empty", r2.Status)
	}
}
