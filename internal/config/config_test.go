package config_test

import (
	"testing"

	"beprod/internal/config"
)

// reset blanks every env var Load reads, so each test starts clean (t.Setenv restores them afterwards).
func reset(t *testing.T) {
	t.Helper()
	for _, k := range []string{"PORT", "DATABASE_URL", "APP_BASE_URL", "PUBLIC_SIGNUP_ENABLED", "SUPER_ADMIN_EMAIL", "GOOGLE_CLIENT_ID", "COOKIE_SECURE"} {
		t.Setenv(k, "")
	}
}

func TestLoad_Defaults(t *testing.T) {
	reset(t)
	t.Setenv("DATABASE_URL", "postgres://x")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want %q", cfg.Port, "8080")
	}
	if cfg.DatabaseURL != "postgres://x" {
		t.Errorf("DatabaseURL = %q, want %q", cfg.DatabaseURL, "postgres://x")
	}
	if cfg.AppBaseURL != "http://localhost:3000" {
		t.Errorf("AppBaseURL = %q, want %q", cfg.AppBaseURL, "http://localhost:3000")
	}
	if cfg.PublicSignupEnabled != false {
		t.Errorf("PublicSignupEnabled = %v, want false", cfg.PublicSignupEnabled)
	}
	if cfg.SuperAdminEmail != "" {
		t.Errorf("SuperAdminEmail = %q, want %q", cfg.SuperAdminEmail, "")
	}
	if cfg.GoogleClientID != "" {
		t.Errorf("GoogleClientID = %q, want %q", cfg.GoogleClientID, "")
	}
	if cfg.CookieSecure != true {
		t.Errorf("CookieSecure = %v, want true", cfg.CookieSecure)
	}
}

func TestLoad_RequiresDatabaseURL(t *testing.T) {
	reset(t)
	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "DATABASE_URL is required" {
		t.Errorf("error = %q, want %q", err.Error(), "DATABASE_URL is required")
	}
}

func TestLoad_AllValues(t *testing.T) {
	reset(t)
	t.Setenv("PORT", "9000")
	t.Setenv("DATABASE_URL", "postgres://db")
	t.Setenv("APP_BASE_URL", "https://beprod.app")
	t.Setenv("PUBLIC_SIGNUP_ENABLED", "true")
	t.Setenv("SUPER_ADMIN_EMAIL", "owner@example.com")
	t.Setenv("GOOGLE_CLIENT_ID", "abc")
	t.Setenv("COOKIE_SECURE", "false")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != "9000" {
		t.Errorf("Port = %q, want %q", cfg.Port, "9000")
	}
	if cfg.DatabaseURL != "postgres://db" {
		t.Errorf("DatabaseURL = %q, want %q", cfg.DatabaseURL, "postgres://db")
	}
	if cfg.AppBaseURL != "https://beprod.app" {
		t.Errorf("AppBaseURL = %q, want %q", cfg.AppBaseURL, "https://beprod.app")
	}
	if cfg.PublicSignupEnabled != true {
		t.Errorf("PublicSignupEnabled = %v, want true", cfg.PublicSignupEnabled)
	}
	if cfg.SuperAdminEmail != "owner@example.com" {
		t.Errorf("SuperAdminEmail = %q, want %q", cfg.SuperAdminEmail, "owner@example.com")
	}
	if cfg.GoogleClientID != "abc" {
		t.Errorf("GoogleClientID = %q, want %q", cfg.GoogleClientID, "abc")
	}
	if cfg.CookieSecure != false {
		t.Errorf("CookieSecure = %v, want false", cfg.CookieSecure)
	}
}

func TestLoad_AppBaseURLTrailingSlash(t *testing.T) {
	reset(t)
	t.Setenv("DATABASE_URL", "postgres://x")

	t.Setenv("APP_BASE_URL", "https://beprod.app/")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AppBaseURL != "https://beprod.app" {
		t.Errorf("AppBaseURL = %q, want %q", cfg.AppBaseURL, "https://beprod.app")
	}

	t.Setenv("APP_BASE_URL", "https://beprod.app//")
	cfg, err = config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AppBaseURL != "https://beprod.app" {
		t.Errorf("AppBaseURL = %q, want %q", cfg.AppBaseURL, "https://beprod.app")
	}
}

func TestLoad_PublicSignupFlag(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"true", true},
		{"TRUE", true},
		{"True", true},
		{"false", false},
		{"1", false},
		{"yes", false},
		{"", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			reset(t)
			t.Setenv("DATABASE_URL", "postgres://x")
			t.Setenv("PUBLIC_SIGNUP_ENABLED", tc.input)
			cfg, err := config.Load()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.PublicSignupEnabled != tc.want {
				t.Errorf("input=%q: PublicSignupEnabled = %v, want %v", tc.input, cfg.PublicSignupEnabled, tc.want)
			}
		})
	}
}

func TestLoad_CookieSecureFlag(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"false", false},
		{"False", false},
		{"true", true},
		{"0", true},
		{"", true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			reset(t)
			t.Setenv("DATABASE_URL", "postgres://x")
			t.Setenv("COOKIE_SECURE", tc.input)
			cfg, err := config.Load()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.CookieSecure != tc.want {
				t.Errorf("input=%q: CookieSecure = %v, want %v", tc.input, cfg.CookieSecure, tc.want)
			}
		})
	}
}

func TestLoad_SuperAdminEmailNormalized(t *testing.T) {
	reset(t)
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("SUPER_ADMIN_EMAIL", "  Owner@Example.COM ")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SuperAdminEmail != "owner@example.com" {
		t.Errorf("SuperAdminEmail = %q, want %q", cfg.SuperAdminEmail, "owner@example.com")
	}
}
