package auth_test

import (
	"context"
	"testing"
	"time"

	"beprod/internal/app"
	"beprod/internal/auth"
	"beprod/internal/models"
	"beprod/internal/testutil"
)

func googleLogin(t *testing.T, a *testutil.TestApp, tok string) testutil.Response {
	t.Helper()
	return a.Do(t, "POST", "/api/v1/auth/google", map[string]any{"idToken": tok}, "")
}

func setGoogle(a *testutil.TestApp, sub, email, name string, verified bool) {
	a.GoogleTokens["tok"] = app.GoogleClaims{Subject: sub, Email: email, Name: name, EmailVerified: verified}
}

func TestGoogle_ExistingActiveUserLinked(t *testing.T) {
	a := testutil.NewApp(t)
	a.CreateUser(t, "ann@example.com")
	setGoogle(a, "sub-1", "Ann@Example.com", "Ann", true)
	r := googleLogin(t, a, "tok")
	if r.Status != 200 {
		t.Fatalf("status %d: %s", r.Status, r.Raw)
	}
	if got := r.Body["user"].(map[string]any)["email"]; got != "ann@example.com" {
		t.Fatalf("email %v", got)
	}
	c := r.Cookie("beprod_session")
	if c == "" {
		t.Fatal("no cookie")
	}
	u := dbUserByEmail(t, a, "ann@example.com")
	if u.GoogleSubject == nil || *u.GoogleSubject != "sub-1" || u.LastLoginAt == nil {
		t.Fatalf("not linked: %+v", u)
	}
	if n := countRows(t, a, &models.User{}); n != 1 {
		t.Fatalf("users %d", n)
	}
	if me := a.Do(t, "GET", "/api/v1/me", nil, "beprod_session="+c); me.Status != 200 {
		t.Fatalf("me %d", me.Status)
	}
}

func TestGoogle_SecondLoginMatchesBySubject(t *testing.T) {
	a := testutil.NewApp(t)
	sub := "sub-1"
	u := a.CreateUserWith(t, auth.NewUser{Name: "Ann", Email: "ann@example.com", GoogleSubject: &sub, Status: models.UserActive})
	setGoogle(a, "sub-1", "changed@example.com", "Ann", true)
	r := googleLogin(t, a, "tok")
	if r.Status != 200 {
		t.Fatalf("status %d: %s", r.Status, r.Raw)
	}
	if r.Body["user"].(map[string]any)["id"] != u.ID.String() {
		t.Fatal("different user")
	}
	if n := countRows(t, a, &models.User{}); n != 1 {
		t.Fatalf("users %d", n)
	}
	if got := dbUserByEmail(t, a, "ann@example.com"); got.ID != u.ID {
		t.Fatal("email changed")
	}
}

func googleActivated(t *testing.T, status, purpose string) {
	a := testutil.NewApp(t)
	u := a.CreateUserWith(t, auth.NewUser{Name: "Inv", Email: "ann@example.com", Status: status})
	if _, err := auth.IssueToken(a.DB, u.ID, purpose, 72*time.Hour); err != nil {
		t.Fatal(err)
	}
	setGoogle(a, "sub-1", "ann@example.com", "Ann", true)
	if r := googleLogin(t, a, "tok"); r.Status != 200 {
		t.Fatalf("status %d: %s", r.Status, r.Raw)
	}
	got := dbUserByEmail(t, a, "ann@example.com")
	if got.Status != models.UserActive || got.GoogleSubject == nil {
		t.Fatalf("not activated: %+v", got)
	}
	var toks []models.UserToken
	if err := a.DB.Where("user_id = ? AND purpose = ?", u.ID, purpose).Find(&toks).Error; err != nil || len(toks) != 1 {
		t.Fatalf("tokens %v %v", toks, err)
	}
	if toks[0].UsedAt == nil {
		t.Fatal("token not used")
	}
}

func TestGoogle_InvitedUserActivated(t *testing.T) {
	googleActivated(t, models.UserInvited, models.TokenInvite)
}

func TestGoogle_PendingVerificationUserActivated(t *testing.T) {
	googleActivated(t, models.UserPendingVerification, models.TokenVerifyEmail)
}

func TestGoogle_UnknownEmail_SignupClosed(t *testing.T) {
	a := testutil.NewApp(t)
	setGoogle(a, "sub-1", "ann@example.com", "Ann", true)
	r := googleLogin(t, a, "tok")
	wantStatus(t, r, 403, "SIGNUP_CLOSED")
	if n := countRows(t, a, &models.User{}); n != 0 {
		t.Fatalf("users %d", n)
	}
	if r.Cookie("beprod_session") != "" {
		t.Fatal("cookie set")
	}
}

func TestGoogle_UnknownEmail_SignupOpen(t *testing.T) {
	a := newSignupOpenApp(t)
	setGoogle(a, "sub-1", "ann@example.com", "  Ann Lee  ", true)
	r := googleLogin(t, a, "tok")
	if r.Status != 200 {
		t.Fatalf("status %d: %s", r.Status, r.Raw)
	}
	u := dbUserByEmail(t, a, "ann@example.com")
	if u.Status != models.UserActive || u.Name != "Ann Lee" || u.PasswordHash != nil ||
		u.GoogleSubject == nil || *u.GoogleSubject != "sub-1" || u.Role != models.RoleUser {
		t.Fatalf("bad user: %+v", u)
	}
	var cats int64
	a.DB.Model(&models.Category{}).Where("user_id = ?", u.ID).Count(&cats)
	if cats != 14 {
		t.Fatalf("categories %d", cats)
	}
	if r.Body["user"].(map[string]any)["hasPassword"] != false {
		t.Fatal("hasPassword")
	}
	if r.Cookie("beprod_session") == "" {
		t.Fatal("no cookie")
	}
	if a.Mailer.Count() != 0 {
		t.Fatal("mail sent")
	}
}

func TestGoogle_NameFallsBackToEmailLocalPart(t *testing.T) {
	a := newSignupOpenApp(t)
	setGoogle(a, "sub-1", "ann@example.com", "", true)
	if r := googleLogin(t, a, "tok"); r.Status != 200 {
		t.Fatalf("status %d: %s", r.Status, r.Raw)
	}
	if got := dbUserByEmail(t, a, "ann@example.com").Name; got != "ann" {
		t.Fatalf("name %q", got)
	}
}

func TestGoogle_EmailNotVerified(t *testing.T) {
	a := newSignupOpenApp(t)
	a.CreateUser(t, "ann@example.com")
	for _, email := range []string{"ann@example.com", "new@example.com"} {
		setGoogle(a, "sub-1", email, "Ann", false)
		r := googleLogin(t, a, "tok")
		wantStatus(t, r, 401, "GOOGLE_EMAIL_NOT_VERIFIED")
		if r.Cookie("beprod_session") != "" {
			t.Fatal("cookie set")
		}
	}
	if n := countRows(t, a, &models.User{}); n != 1 {
		t.Fatalf("users %d", n)
	}
	if dbUserByEmail(t, a, "ann@example.com").GoogleSubject != nil {
		t.Fatal("subject linked")
	}
}

func TestGoogle_UnknownToken(t *testing.T) {
	a := testutil.NewApp(t)
	wantStatus(t, googleLogin(t, a, "nope"), 401, "INVALID_GOOGLE_TOKEN")
}

func TestGoogle_Deactivated(t *testing.T) {
	for name, sub := range map[string]*string{"bySubject": ptr("sub-1"), "byEmail": nil} {
		t.Run(name, func(t *testing.T) {
			a := testutil.NewApp(t)
			a.CreateUserWith(t, auth.NewUser{Name: "Ann", Email: "ann@example.com", GoogleSubject: sub, Status: models.UserDeactivated})
			setGoogle(a, "sub-1", "ann@example.com", "Ann", true)
			r := googleLogin(t, a, "tok")
			wantStatus(t, r, 403, "ACCOUNT_DEACTIVATED")
			if r.Cookie("beprod_session") != "" {
				t.Fatal("cookie set")
			}
		})
	}
}

func TestGoogle_DifferentSubjectConflict(t *testing.T) {
	a := testutil.NewApp(t)
	a.CreateUserWith(t, auth.NewUser{Name: "Ann", Email: "ann@example.com", GoogleSubject: ptr("other-sub"), Status: models.UserActive})
	setGoogle(a, "sub-1", "ann@example.com", "Ann", true)
	r := googleLogin(t, a, "tok")
	wantStatus(t, r, 409, "GOOGLE_ACCOUNT_CONFLICT")
	if r.Cookie("beprod_session") != "" {
		t.Fatal("cookie set")
	}
	if s := dbUserByEmail(t, a, "ann@example.com").GoogleSubject; s == nil || *s != "other-sub" {
		t.Fatalf("subject %v", s)
	}
}

func TestGoogle_MissingIDToken(t *testing.T) {
	a := testutil.NewApp(t)
	for _, body := range []map[string]any{{}, {"idToken": "   "}} {
		r := a.Do(t, "POST", "/api/v1/auth/google", body, "")
		wantStatus(t, r, 400, "VALIDATION_ERROR")
		if r.Field("idToken") != "idToken is required" {
			t.Fatalf("field %q", r.Field("idToken"))
		}
	}
}

func TestNewGoogleVerifier_EmptyClientIDAlwaysFails(t *testing.T) {
	if _, err := auth.NewGoogleVerifier("")(context.Background(), "whatever"); err == nil {
		t.Fatal("expected error")
	}
}
