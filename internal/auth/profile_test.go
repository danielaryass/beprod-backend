package auth_test

import (
	"strings"
	"testing"

	"beprod/internal/auth"
	"beprod/internal/models"
	"beprod/internal/testutil"
)

const (
	mePath    = "/api/v1/me"
	mePwdPath = "/api/v1/me/password"
	nameMsg   = "Name must be 1–60 characters"
)

func dbName(t *testing.T, a *testutil.TestApp, u models.User) string {
	t.Helper()
	var got models.User
	if err := a.DB.First(&got, "id = ?", u.ID).Error; err != nil {
		t.Fatal(err)
	}
	return got.Name
}

func TestUpdateMe_TrimsName(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "p@example.com")
	r := a.Do(t, "PATCH", mePath, map[string]any{"name": "  Budi Santoso  "}, cookie)
	if r.Status != 200 || r.Body["user"].(map[string]any)["name"] != "Budi Santoso" {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
	if dbName(t, a, u) != "Budi Santoso" {
		t.Fatal("db name not updated")
	}
}

func TestUpdateMe_Invalid(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "p@example.com")
	for _, n := range []string{"   ", strings.Repeat("a", 61)} {
		r := a.Do(t, "PATCH", mePath, map[string]any{"name": n}, cookie)
		wantStatus(t, r, 400, "VALIDATION_ERROR")
		if r.Field("name") != nameMsg {
			t.Fatalf("field %q", r.Field("name"))
		}
	}
	if dbName(t, a, u) != "Test User" {
		t.Fatal("name changed")
	}
	if r := a.Do(t, "PATCH", mePath, map[string]any{"name": strings.Repeat("a", 60)}, cookie); r.Status != 200 {
		t.Fatalf("60 chars: %d", r.Status)
	}
}

func TestUpdateMe_RequiresLogin(t *testing.T) {
	a := testutil.NewApp(t)
	if r := a.Do(t, "PATCH", mePath, map[string]any{"name": "X"}, ""); r.Status != 401 {
		t.Fatalf("got %d", r.Status)
	}
}

func TestUpdateMe_DoesNotChangeEmail(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "p@example.com")
	if r := a.Do(t, "PATCH", mePath, map[string]any{"name": "X", "email": "hack@example.com"}, cookie); r.Status != 200 {
		t.Fatalf("got %d", r.Status)
	}
	var got models.User
	if err := a.DB.First(&got, "id = ?", u.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Email != "p@example.com" {
		t.Fatalf("email %q", got.Email)
	}
}

func TestChangePassword_OK(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := a.NewUser(t, "p@example.com")
	r := a.Do(t, "POST", mePwdPath, map[string]any{"currentPassword": "password1", "newPassword": resetNewPwd}, cookie)
	if r.Status != 204 {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
	if r := login(t, a, "p@example.com", resetNewPwd); r.Status != 200 {
		t.Fatalf("login new %d", r.Status)
	}
	if r := login(t, a, "p@example.com", "password1"); r.Status != 401 {
		t.Fatalf("login old %d", r.Status)
	}
	if r := a.Do(t, "GET", mePath, nil, cookie); r.Status != 200 {
		t.Fatalf("old session %d", r.Status)
	}
}

func TestChangePassword_WrongCurrent(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := a.NewUser(t, "p@example.com")
	r := a.Do(t, "POST", mePwdPath, map[string]any{"currentPassword": "wrongpass1", "newPassword": resetNewPwd}, cookie)
	wantStatus(t, r, 400, "VALIDATION_ERROR")
	if r.Field("currentPassword") != "Current password is incorrect" {
		t.Fatalf("field %q", r.Field("currentPassword"))
	}
	if r := login(t, a, "p@example.com", "password1"); r.Status != 200 {
		t.Fatalf("password changed: %d", r.Status)
	}
}

func TestChangePassword_WeakNew(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := a.NewUser(t, "p@example.com")
	r := a.Do(t, "POST", mePwdPath, map[string]any{"currentPassword": "password1", "newPassword": "short"}, cookie)
	wantStatus(t, r, 400, "VALIDATION_ERROR")
	if r.Field("newPassword") != weakPwdMsg || r.Field("currentPassword") != "" {
		t.Fatalf("fields %s", r.Raw)
	}
}

func TestChangePassword_BothWrong(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := a.NewUser(t, "p@example.com")
	r := a.Do(t, "POST", mePwdPath, map[string]any{"currentPassword": "wrongpass1", "newPassword": "short"}, cookie)
	wantStatus(t, r, 400, "VALIDATION_ERROR")
	if r.Field("newPassword") == "" || r.Field("currentPassword") == "" {
		t.Fatalf("fields %s", r.Raw)
	}
}

func TestChangePassword_NoPassword(t *testing.T) {
	a := testutil.NewApp(t)
	sub := "g-sub-2"
	u := a.CreateUserWith(t, auth.NewUser{Name: "G", Email: "g@example.com", GoogleSubject: &sub, Status: models.UserActive})
	cookie := a.LoginAs(t, u.ID)
	r := a.Do(t, "POST", mePwdPath, map[string]any{"currentPassword": "x", "newPassword": resetNewPwd}, cookie)
	wantStatus(t, r, 400, "NO_PASSWORD")
	if r.Body["error"].(map[string]any)["message"] != "You signed up with Google. Use 'Forgot password' to set a password." {
		t.Fatalf("message %s", r.Raw)
	}
}

func TestChangePassword_RequiresLogin(t *testing.T) {
	a := testutil.NewApp(t)
	if r := a.Do(t, "POST", mePwdPath, map[string]any{"currentPassword": "x", "newPassword": resetNewPwd}, ""); r.Status != 401 {
		t.Fatalf("got %d", r.Status)
	}
}

func dbPayday(t *testing.T, a *testutil.TestApp, u models.User) int {
	t.Helper()
	var got models.User
	if err := a.DB.First(&got, "id = ?", u.ID).Error; err != nil {
		t.Fatal(err)
	}
	return got.Payday
}

func TestUpdateMe_Payday(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "p@example.com")
	r := a.Do(t, "PATCH", mePath, map[string]any{"payday": 1}, cookie)
	user, _ := r.Body["user"].(map[string]any)
	if r.Status != 200 || user["payday"] != 1.0 || user["name"] != "Test User" {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
	if dbPayday(t, a, u) != 1 || dbName(t, a, u) != "Test User" {
		t.Fatal("db not updated")
	}
	for _, p := range []int{0, 32} {
		r := a.Do(t, "PATCH", mePath, map[string]any{"payday": p}, cookie)
		wantStatus(t, r, 400, "VALIDATION_ERROR")
		if r.Field("payday") != "Choose a day from 1 to 31" {
			t.Fatalf("field %q", r.Field("payday"))
		}
	}
	r = a.Do(t, "PATCH", mePath, map[string]any{"name": "New"}, cookie)
	if r.Status != 200 || dbName(t, a, u) != "New" || dbPayday(t, a, u) != 1 {
		t.Fatalf("name patch: %d %s", r.Status, r.Raw)
	}
}
