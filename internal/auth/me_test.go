package auth_test

import (
	"testing"

	"beprod/internal/auth"
	"beprod/internal/config"
	"beprod/internal/models"
	"beprod/internal/testutil"
)

func ptr(s string) *string { return &s }

func TestMe_Unauthenticated401(t *testing.T) {
	a := testutil.NewApp(t)
	r := a.Do(t, "GET", "/api/v1/me", nil, "")
	if r.Status != 401 {
		t.Errorf("want 401, got %d", r.Status)
	}
	if r.ErrCode() != "UNAUTHENTICATED" {
		t.Errorf("want UNAUTHENTICATED, got %q", r.ErrCode())
	}
}

func TestMe_ReturnsDTO(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "me@x.com")
	r := a.Do(t, "GET", "/api/v1/me", nil, cookie)
	if r.Status != 200 {
		t.Errorf("want 200, got %d", r.Status)
	}
	user, _ := r.Body["user"].(map[string]any)
	if user["id"] != u.ID.String() {
		t.Errorf("id: want %q, got %v", u.ID.String(), user["id"])
	}
	if user["name"] != "Test User" {
		t.Errorf("name: want 'Test User', got %v", user["name"])
	}
	if user["email"] != "me@x.com" {
		t.Errorf("email: want 'me@x.com', got %v", user["email"])
	}
	if user["role"] != "user" {
		t.Errorf("role: want 'user', got %v", user["role"])
	}
	if user["status"] != "active" {
		t.Errorf("status: want 'active', got %v", user["status"])
	}
	if user["hasPassword"] != true {
		t.Errorf("hasPassword: want true, got %v", user["hasPassword"])
	}
	createdAt, _ := user["createdAt"].(string)
	if createdAt == "" {
		t.Error("createdAt should not be empty")
	}
	// Ensure no secret fields
	for _, key := range []string{"passwordHash", "PasswordHash", "password", "googleSubject"} {
		if _, ok := user[key]; ok {
			t.Errorf("response should not contain key %q", key)
		}
	}
}

func TestMe_GoogleOnlyUserHasPasswordFalse(t *testing.T) {
	a := testutil.NewApp(t)
	u := a.CreateUserWith(t, auth.NewUser{Name: "G", Email: "g@x.com", GoogleSubject: ptr("sub-1"), Status: models.UserActive})
	cookie := a.LoginAs(t, u.ID)
	r := a.Do(t, "GET", "/api/v1/me", nil, cookie)
	if r.Status != 200 {
		t.Errorf("want 200, got %d", r.Status)
	}
	user, _ := r.Body["user"].(map[string]any)
	if user["hasPassword"] != false {
		t.Errorf("hasPassword: want false, got %v", user["hasPassword"])
	}
}

func TestMe_SuperAdminRole(t *testing.T) {
	a := testutil.NewApp(t)
	u := a.CreateUserWith(t, auth.NewUser{Name: "SA", Email: "sa@x.com", Role: models.RoleSuperAdmin, Status: models.UserActive})
	cookie := a.LoginAs(t, u.ID)
	r := a.Do(t, "GET", "/api/v1/me", nil, cookie)
	if r.Status != 200 {
		t.Errorf("want 200, got %d", r.Status)
	}
	user, _ := r.Body["user"].(map[string]any)
	if user["role"] != "super_admin" {
		t.Errorf("role: want 'super_admin', got %v", user["role"])
	}
}

func TestToMeDTO_EmptyPasswordHashIsFalse(t *testing.T) {
	emptyString := ""
	dto1 := auth.ToMeDTO(models.User{PasswordHash: &emptyString})
	if dto1.HasPassword {
		t.Error("empty password hash: HasPassword should be false")
	}
	dto2 := auth.ToMeDTO(models.User{})
	if dto2.HasPassword {
		t.Error("nil password hash: HasPassword should be false")
	}
}

func TestConfig_DefaultSignupDisabled(t *testing.T) {
	a := testutil.NewApp(t)
	r := a.Do(t, "GET", "/api/v1/config", nil, "")
	if r.Status != 200 {
		t.Errorf("want 200, got %d", r.Status)
	}
	if r.Body["publicSignupEnabled"] != false {
		t.Errorf("publicSignupEnabled: want false, got %v", r.Body["publicSignupEnabled"])
	}
}

func TestConfig_ReflectsFlag(t *testing.T) {
	a := testutil.NewApp(t, func(c *config.Config) { c.PublicSignupEnabled = true })
	r := a.Do(t, "GET", "/api/v1/config", nil, "")
	if r.Status != 200 {
		t.Errorf("want 200, got %d", r.Status)
	}
	if r.Body["publicSignupEnabled"] != true {
		t.Errorf("publicSignupEnabled: want true, got %v", r.Body["publicSignupEnabled"])
	}
}

func TestConfig_NoAuthRequired(t *testing.T) {
	a := testutil.NewApp(t)
	r := a.Do(t, "GET", "/api/v1/config", nil, "")
	if r.Status != 200 {
		t.Errorf("want 200 (no auth needed), got %d", r.Status)
	}
}
