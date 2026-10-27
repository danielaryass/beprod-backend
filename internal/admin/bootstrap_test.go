package admin_test

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"

	"beprod/internal/admin"
	"beprod/internal/auth"
	"beprod/internal/config"
	"beprod/internal/models"
	"beprod/internal/testutil"
)

func countUsers(t *testing.T, a *testutil.TestApp) int64 {
	t.Helper()
	var n int64
	if err := a.DB.Model(&models.User{}).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func bootApp(t *testing.T) *testutil.TestApp {
	t.Helper()
	return testutil.NewApp(t, func(c *config.Config) { c.SuperAdminEmail = "owner@example.com" })
}

func inviteTokens(t *testing.T, a *testutil.TestApp) []models.UserToken {
	t.Helper()
	var toks []models.UserToken
	if err := a.DB.Where("purpose = ?", models.TokenInvite).Order("created_at, id").Find(&toks).Error; err != nil {
		t.Fatal(err)
	}
	return toks
}

func userByEmail(t *testing.T, a *testutil.TestApp, email string) models.User {
	t.Helper()
	var u models.User
	if err := a.DB.Where("email = ?", email).First(&u).Error; err != nil {
		t.Fatal(err)
	}
	return u
}

func TestBootstrap_EmptyEmailNoop(t *testing.T) {
	a := testutil.NewApp(t)
	if err := admin.Bootstrap(a.DB, a.Config); err != nil {
		t.Fatal(err)
	}
	if n := countUsers(t, a); n != 0 {
		t.Fatalf("users = %d", n)
	}
}

func TestBootstrap_CreatesSuperAdminWithDefaultPassword(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	a := bootApp(t)
	if err := admin.Bootstrap(a.DB, a.Config); err != nil {
		t.Fatal(err)
	}
	if n := countUsers(t, a); n != 1 {
		t.Fatalf("users = %d", n)
	}
	u := userByEmail(t, a, "owner@example.com")
	if u.Role != models.RoleSuperAdmin || u.Status != models.UserActive || u.Name != "owner" || u.PasswordHash == nil {
		t.Fatalf("user = %+v", u)
	}
	var cats int64
	a.DB.Model(&models.Category{}).Where("user_id = ?", u.ID).Count(&cats)
	if cats != 14 {
		t.Fatalf("categories = %d", cats)
	}
	if n := len(inviteTokens(t, a)); n != 0 {
		t.Fatalf("tokens = %d", n)
	}
	if r := a.Do(t, "POST", "/api/v1/auth/login", map[string]any{"email": "owner@example.com", "password": admin.DefaultPassword}, ""); r.Status != 200 {
		t.Fatalf("login: %d %s", r.Status, r.Raw)
	}
	if !strings.Contains(buf.String(), "owner@example.com") || !strings.Contains(buf.String(), "default password") {
		t.Fatalf("log = %q", buf.String())
	}
}

func TestBootstrap_Idempotent(t *testing.T) {
	a := bootApp(t)
	if err := admin.Bootstrap(a.DB, a.Config); err != nil {
		t.Fatal(err)
	}
	// the owner changes their password; a restart must not reset it
	if err := a.DB.Model(&models.User{}).Where("email = ?", "owner@example.com").Update("password_hash", "changed").Error; err != nil {
		t.Fatal(err)
	}
	if err := admin.Bootstrap(a.DB, a.Config); err != nil {
		t.Fatal(err)
	}
	var supers int64
	a.DB.Model(&models.User{}).Where("role = ?", models.RoleSuperAdmin).Count(&supers)
	if n := countUsers(t, a); n != 1 || supers != 1 {
		t.Fatalf("users = %d supers = %d", n, supers)
	}
	if u := userByEmail(t, a, "owner@example.com"); u.PasswordHash == nil || *u.PasswordHash != "changed" {
		t.Fatal("restart overwrote the password")
	}
}

func TestBootstrap_ActivatesInvitedOwner(t *testing.T) {
	a := bootApp(t)
	a.CreateUserWith(t, auth.NewUser{Name: "O", Email: "owner@example.com", Status: models.UserInvited})
	if err := admin.Bootstrap(a.DB, a.Config); err != nil {
		t.Fatal(err)
	}
	if u := userByEmail(t, a, "owner@example.com"); u.Status != models.UserActive || u.PasswordHash == nil {
		t.Fatalf("user = %+v", u)
	}
}

func TestBootstrap_PromotesExistingUser(t *testing.T) {
	a := bootApp(t)
	u := a.CreateUser(t, "owner@example.com")
	if err := admin.Bootstrap(a.DB, a.Config); err != nil {
		t.Fatal(err)
	}
	got := userByEmail(t, a, "owner@example.com")
	if got.Role != models.RoleSuperAdmin || got.Status != models.UserActive {
		t.Fatalf("user = %+v", got)
	}
	if got.PasswordHash == nil || u.PasswordHash == nil || *got.PasswordHash != *u.PasswordHash {
		t.Fatal("password changed")
	}
	if n := len(inviteTokens(t, a)); n != 0 {
		t.Fatalf("tokens = %d", n)
	}
	if n := countUsers(t, a); n != 1 {
		t.Fatalf("users = %d", n)
	}
}

func TestBootstrap_ChangingEmailDemotesOld(t *testing.T) {
	a := bootApp(t)
	boot := func(email string) {
		t.Helper()
		cfg := a.Config
		cfg.SuperAdminEmail = email
		if err := admin.Bootstrap(a.DB, cfg); err != nil {
			t.Fatal(err)
		}
	}
	check := func(super, other string) {
		t.Helper()
		if r := userByEmail(t, a, super).Role; r != models.RoleSuperAdmin {
			t.Fatalf("%s role = %s", super, r)
		}
		if r := userByEmail(t, a, other).Role; r != models.RoleUser {
			t.Fatalf("%s role = %s", other, r)
		}
		var n int64
		a.DB.Model(&models.User{}).Where("role = ?", models.RoleSuperAdmin).Count(&n)
		if n != 1 {
			t.Fatalf("super admins = %d", n)
		}
	}
	boot("old@example.com")
	boot("new@example.com")
	check("new@example.com", "old@example.com")
	boot("old@example.com")
	check("old@example.com", "new@example.com")
}
