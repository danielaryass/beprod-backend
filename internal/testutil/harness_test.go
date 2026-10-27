package testutil_test

import (
	"net/http"
	"testing"

	"beprod/internal/app"
	"beprod/internal/config"
	"beprod/internal/mail"
	"beprod/internal/models"
	"beprod/internal/secure"
	"beprod/internal/testutil"
)

func TestNewApp_Defaults(t *testing.T) {
	a := testutil.NewApp(t)
	if a.Config.AppBaseURL != "http://app.test" {
		t.Errorf("AppBaseURL: want 'http://app.test', got %q", a.Config.AppBaseURL)
	}
	if a.Config.PublicSignupEnabled {
		t.Error("PublicSignupEnabled: want false")
	}
	if a.Config.SuperAdminEmail != "" {
		t.Errorf("SuperAdminEmail: want '', got %q", a.Config.SuperAdminEmail)
	}
	if a.Config.GoogleClientID != "test-client" {
		t.Errorf("GoogleClientID: want 'test-client', got %q", a.Config.GoogleClientID)
	}
	if a.Config.CookieSecure {
		t.Error("CookieSecure: want false")
	}
	if a.Mailer == nil {
		t.Error("Mailer: want non-nil")
	}
	if a.DB == nil {
		t.Error("DB: want non-nil")
	}
	if a.GoogleTokens == nil {
		t.Error("GoogleTokens: want non-nil")
	}
}

func TestNewApp_OptsApplied(t *testing.T) {
	a := testutil.NewApp(t, func(c *config.Config) {
		c.PublicSignupEnabled = true
		c.AppBaseURL = "http://x.test"
	})
	if !a.Config.PublicSignupEnabled {
		t.Error("PublicSignupEnabled: want true")
	}
	if a.Config.AppBaseURL != "http://x.test" {
		t.Errorf("AppBaseURL: want 'http://x.test', got %q", a.Config.AppBaseURL)
	}
	r := a.Do(t, "GET", "/api/v1/config", nil, "")
	if r.Status != 200 {
		t.Errorf("want 200, got %d", r.Status)
	}
	if r.Body["publicSignupEnabled"] != true {
		t.Errorf("publicSignupEnabled: want true, got %v", r.Body["publicSignupEnabled"])
	}
}

func TestDo_SendsCSRFHeader(t *testing.T) {
	a := testutil.NewApp(t)
	r := a.Do(t, "POST", "/api/v1/nope", map[string]string{"a": "b"}, "")
	if r.Status != 404 {
		t.Errorf("want 404 (not 403), got %d", r.Status)
	}
}

func TestDoH_EmptyValueRemovesHeader(t *testing.T) {
	a := testutil.NewApp(t)
	r := a.DoH(t, "POST", "/api/v1/nope", nil, map[string]string{"X-BeProd-CSRF": ""})
	if r.Status != 403 {
		t.Errorf("want 403, got %d", r.Status)
	}
	if r.ErrCode() != "CSRF_REQUIRED" {
		t.Errorf("want CSRF_REQUIRED, got %q", r.ErrCode())
	}
}

func TestDoH_CustomHeaderOverride(t *testing.T) {
	a := testutil.NewApp(t)
	r := a.DoH(t, "POST", "/api/v1/nope", nil, map[string]string{"X-BeProd-CSRF": "0"})
	if r.Status != 403 {
		t.Errorf("want 403, got %d", r.Status)
	}
}

func TestDo_NoBodyNoContentType(t *testing.T) {
	a := testutil.NewApp(t)
	r := a.Do(t, "GET", "/healthz", nil, "")
	if r.Status != 200 {
		t.Errorf("want 200, got %d", r.Status)
	}
	if r.Body["status"] != "ok" {
		t.Errorf("status: want 'ok', got %v", r.Body["status"])
	}
}

func TestResponse_Helpers(t *testing.T) {
	r := testutil.Response{
		Status: 400,
		Body: map[string]any{
			"error": map[string]any{
				"code": "X",
				"fields": map[string]any{
					"name": "bad",
				},
			},
		},
		Raw:    []byte(`{"a":1}`),
		Header: http.Header{"Set-Cookie": {"beprod_session=abc; Path=/; HttpOnly"}},
	}
	if r.ErrCode() != "X" {
		t.Errorf("ErrCode: want 'X', got %q", r.ErrCode())
	}
	if r.Field("name") != "bad" {
		t.Errorf("Field(name): want 'bad', got %q", r.Field("name"))
	}
	if r.Field("missing") != "" {
		t.Errorf("Field(missing): want '', got %q", r.Field("missing"))
	}
	if r.Cookie("beprod_session") != "abc" {
		t.Errorf("Cookie(beprod_session): want 'abc', got %q", r.Cookie("beprod_session"))
	}
	if r.Cookie("other") != "" {
		t.Errorf("Cookie(other): want '', got %q", r.Cookie("other"))
	}
	var v struct {
		A int `json:"a"`
	}
	r.Decode(t, &v)
	if v.A != 1 {
		t.Errorf("Decode: want v.A==1, got %d", v.A)
	}
	// Empty body
	r2 := testutil.Response{Body: nil}
	if r2.ErrCode() != "" {
		t.Errorf("empty body ErrCode: want '', got %q", r2.ErrCode())
	}
	if r2.Field("x") != "" {
		t.Errorf("empty body Field: want '', got %q", r2.Field("x"))
	}
}

func TestCreateUser_PasswordAndSeed(t *testing.T) {
	a := testutil.NewApp(t)
	u := a.CreateUser(t, "c@x.com")
	if u.Status != "active" {
		t.Errorf("status: want 'active', got %q", u.Status)
	}
	if u.Role != "user" {
		t.Errorf("role: want 'user', got %q", u.Role)
	}
	if u.PasswordHash == nil || !secure.CheckPassword(*u.PasswordHash, testutil.Password) {
		t.Error("password hash does not match testutil.Password")
	}
	var count int64
	a.DB.Model(&models.Category{}).Where("user_id = ?", u.ID).Count(&count)
	if count != 14 {
		t.Errorf("categories: want 14, got %d", count)
	}
}

func TestNewUser_CookieWorks(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := a.NewUser(t, "n@x.com")
	if len(cookie) < len("beprod_session=") || cookie[:len("beprod_session=")] != "beprod_session=" {
		t.Errorf("cookie should start with 'beprod_session=', got %q", cookie)
	}
	r := a.Do(t, "GET", "/api/v1/me", nil, cookie)
	if r.Status != 200 {
		t.Errorf("want 200, got %d", r.Status)
	}
}

func TestMailToken_ExtractsToken(t *testing.T) {
	tok := testutil.MailToken(t, mail.Message{Body: "Open http://app.test/verify-email?token=abc_DEF-123 now"})
	if tok != "abc_DEF-123" {
		t.Errorf("want 'abc_DEF-123', got %q", tok)
	}
}

func TestGoogleTokensMapExists(t *testing.T) {
	a := testutil.NewApp(t)
	a.GoogleTokens["t"] = app.GoogleClaims{Subject: "s", Email: "e@x.com", Name: "N", EmailVerified: true}
	cl := a.GoogleTokens["t"]
	if cl.Subject != "s" {
		t.Errorf("want Subject 's', got %q", cl.Subject)
	}
}
