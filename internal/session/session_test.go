package session_test

import (
	"strings"
	"testing"
	"time"

	"beprod/internal/config"
	"beprod/internal/models"
	"beprod/internal/secure"
	"beprod/internal/session"
	"beprod/internal/testutil"
)

func containsCI(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

func TestCreate_StoresHashNotPlain(t *testing.T) {
	fixed := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	a := testutil.NewApp(t)
	testutil.SetNow(t, fixed)
	u := a.CreateUser(t, "u@x.com")
	tok, err := session.Create(a.DB, u.ID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(tok) != 43 {
		t.Errorf("want token length 43, got %d", len(tok))
	}
	var s models.Session
	a.DB.Where("user_id = ?", u.ID).First(&s)
	if s.TokenHash != secure.HashToken(tok) {
		t.Errorf("TokenHash mismatch")
	}
	if s.TokenHash == tok {
		t.Errorf("TokenHash should not equal plain token")
	}
	if !s.LastSeenAt.Equal(fixed) {
		t.Errorf("LastSeenAt: want %v, got %v", fixed, s.LastSeenAt)
	}
}

func TestRequire_NoCookie401(t *testing.T) {
	a := testutil.NewApp(t)
	r := a.Do(t, "GET", "/api/v1/me", nil, "")
	if r.Status != 401 {
		t.Errorf("want 401, got %d", r.Status)
	}
	if r.ErrCode() != "UNAUTHENTICATED" {
		t.Errorf("want UNAUTHENTICATED, got %q", r.ErrCode())
	}
	e, _ := r.Body["error"].(map[string]any)
	msg, _ := e["message"].(string)
	if msg != "Please log in" {
		t.Errorf("want 'Please log in', got %q", msg)
	}
}

func TestRequire_UnknownToken401(t *testing.T) {
	a := testutil.NewApp(t)
	r := a.Do(t, "GET", "/api/v1/me", nil, "beprod_session=garbage")
	if r.Status != 401 {
		t.Errorf("want 401, got %d", r.Status)
	}
	if r.ErrCode() != "UNAUTHENTICATED" {
		t.Errorf("want UNAUTHENTICATED, got %q", r.ErrCode())
	}
}

func TestRequire_ValidCookie200(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u@x.com")
	r := a.Do(t, "GET", "/api/v1/me", nil, cookie)
	if r.Status != 200 {
		t.Errorf("want 200, got %d", r.Status)
	}
	user, _ := r.Body["user"].(map[string]any)
	email, _ := user["email"].(string)
	if email != u.Email {
		t.Errorf("want email %q, got %q", u.Email, email)
	}
}

func TestRequire_ExpiredAfter30Days(t *testing.T) {
	fixed := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	a := testutil.NewApp(t)
	testutil.SetNow(t, fixed)
	u, cookie := a.NewUser(t, "u@x.com")
	testutil.SetNow(t, fixed.Add(30*24*time.Hour+time.Second))
	r := a.Do(t, "GET", "/api/v1/me", nil, cookie)
	if r.Status != 401 {
		t.Errorf("want 401, got %d", r.Status)
	}
	var count int64
	a.DB.Model(&models.Session{}).Where("user_id = ?", u.ID).Count(&count)
	if count != 0 {
		t.Errorf("want 0 sessions, got %d", count)
	}
	// Second request with same cookie
	r2 := a.Do(t, "GET", "/api/v1/me", nil, cookie)
	if r2.Status != 401 {
		t.Errorf("second request: want 401, got %d", r2.Status)
	}
}

func TestRequire_Exactly30DaysStillValid(t *testing.T) {
	fixed := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	a := testutil.NewApp(t)
	testutil.SetNow(t, fixed)
	_, cookie := a.NewUser(t, "u@x.com")
	testutil.SetNow(t, fixed.Add(30*24*time.Hour))
	r := a.Do(t, "GET", "/api/v1/me", nil, cookie)
	if r.Status != 200 {
		t.Errorf("want 200 at exactly 30 days, got %d", r.Status)
	}
}

func TestRequire_DeactivatedUser401(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u@x.com")
	a.DB.Model(&models.User{}).Where("id = ?", u.ID).Update("status", models.UserDeactivated)
	r := a.Do(t, "GET", "/api/v1/me", nil, cookie)
	if r.Status != 401 {
		t.Errorf("want 401, got %d", r.Status)
	}
	// Reactivate
	a.DB.Model(&models.User{}).Where("id = ?", u.ID).Update("status", models.UserActive)
	r2 := a.Do(t, "GET", "/api/v1/me", nil, cookie)
	if r2.Status != 200 {
		t.Errorf("want 200 after reactivation, got %d", r2.Status)
	}
}

func TestRequire_LastSeenRefreshedAfterOneMinute(t *testing.T) {
	fixed := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	a := testutil.NewApp(t)
	testutil.SetNow(t, fixed)
	u, cookie := a.NewUser(t, "u@x.com")
	testutil.SetNow(t, fixed.Add(2*time.Minute))
	r := a.Do(t, "GET", "/api/v1/me", nil, cookie)
	if r.Status != 200 {
		t.Errorf("want 200, got %d", r.Status)
	}
	var s models.Session
	a.DB.Where("user_id = ?", u.ID).First(&s)
	if !s.LastSeenAt.Equal(fixed.Add(2 * time.Minute)) {
		t.Errorf("LastSeenAt: want %v, got %v", fixed.Add(2*time.Minute), s.LastSeenAt)
	}
	// Cookie value in response should match token from request
	plainToken := strings.TrimPrefix(cookie, session.CookieName+"=")
	if r.Cookie(session.CookieName) != plainToken {
		t.Errorf("response cookie: want %q, got %q", plainToken, r.Cookie(session.CookieName))
	}
}

func TestRequire_NoRefreshWithinOneMinute(t *testing.T) {
	fixed := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	a := testutil.NewApp(t)
	testutil.SetNow(t, fixed)
	u, cookie := a.NewUser(t, "u@x.com")
	testutil.SetNow(t, fixed.Add(30*time.Second))
	r := a.Do(t, "GET", "/api/v1/me", nil, cookie)
	if r.Status != 200 {
		t.Errorf("want 200, got %d", r.Status)
	}
	var s models.Session
	a.DB.Where("user_id = ?", u.ID).First(&s)
	if !s.LastSeenAt.Equal(fixed) {
		t.Errorf("LastSeenAt should not change, got %v", s.LastSeenAt)
	}
	if r.Cookie(session.CookieName) != "" {
		t.Errorf("want no Set-Cookie, got %q", r.Cookie(session.CookieName))
	}
}

func TestRequire_RefreshedCookieAttributes(t *testing.T) {
	fixed := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	// Test with CookieSecure=false
	a := testutil.NewApp(t)
	testutil.SetNow(t, fixed)
	_, cookie := a.NewUser(t, "u@x.com")
	testutil.SetNow(t, fixed.Add(2*time.Minute))
	r := a.Do(t, "GET", "/api/v1/me", nil, cookie)
	if r.Status != 200 {
		t.Errorf("want 200, got %d", r.Status)
	}
	setCookie := strings.Join(r.Header.Values("Set-Cookie"), ";")
	if !containsCI(setCookie, "HttpOnly") {
		t.Errorf("want HttpOnly in Set-Cookie, got: %s", setCookie)
	}
	if !containsCI(setCookie, "SameSite=Lax") {
		t.Errorf("want SameSite=Lax in Set-Cookie, got: %s", setCookie)
	}
	if !containsCI(setCookie, "Path=/") {
		t.Errorf("want Path=/ in Set-Cookie, got: %s", setCookie)
	}
	if containsCI(setCookie, "Secure") {
		t.Errorf("want no Secure in Set-Cookie when CookieSecure=false, got: %s", setCookie)
	}

	// Test with CookieSecure=true
	a2 := testutil.NewApp(t, func(c *config.Config) { c.CookieSecure = true })
	testutil.SetNow(t, fixed)
	_, cookie2 := a2.NewUser(t, "u2@x.com")
	testutil.SetNow(t, fixed.Add(2*time.Minute))
	r2 := a2.Do(t, "GET", "/api/v1/me", nil, cookie2)
	if r2.Status != 200 {
		t.Errorf("secure app: want 200, got %d", r2.Status)
	}
	setCookie2 := strings.Join(r2.Header.Values("Set-Cookie"), ";")
	if !containsCI(setCookie2, "Secure") {
		t.Errorf("want Secure in Set-Cookie when CookieSecure=true, got: %s", setCookie2)
	}
}

func TestRevokeAll_OnlyThatUser(t *testing.T) {
	a := testutil.NewApp(t)
	u1, cookie1a := a.NewUser(t, "u1@x.com")
	cookie1b := a.LoginAs(t, u1.ID) // second session for u1
	u2, cookie2a := a.NewUser(t, "u2@x.com")
	_ = a.LoginAs(t, u2.ID) // second session for u2

	if err := session.RevokeAll(a.DB, u1.ID); err != nil {
		t.Fatalf("RevokeAll: %v", err)
	}

	var count1, count2 int64
	a.DB.Model(&models.Session{}).Where("user_id = ?", u1.ID).Count(&count1)
	a.DB.Model(&models.Session{}).Where("user_id = ?", u2.ID).Count(&count2)
	if count1 != 0 {
		t.Errorf("u1 sessions: want 0, got %d", count1)
	}
	if count2 != 2 {
		t.Errorf("u2 sessions: want 2, got %d", count2)
	}
	// u1 cookies should be 401
	r1 := a.Do(t, "GET", "/api/v1/me", nil, cookie1a)
	if r1.Status != 401 {
		t.Errorf("cookie1a: want 401, got %d", r1.Status)
	}
	r1b := a.Do(t, "GET", "/api/v1/me", nil, cookie1b)
	if r1b.Status != 401 {
		t.Errorf("cookie1b: want 401, got %d", r1b.Status)
	}
	// u2 cookie should be 200
	r2 := a.Do(t, "GET", "/api/v1/me", nil, cookie2a)
	if r2.Status != 200 {
		t.Errorf("cookie2a: want 200, got %d", r2.Status)
	}
}
