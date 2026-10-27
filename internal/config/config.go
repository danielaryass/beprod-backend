package config

import (
	"errors"
	"os"
	"strings"
)

// Config holds all application configuration loaded from environment variables.
type Config struct {
	Port                string // PORT, default "8080"
	DatabaseURL         string // DATABASE_URL, required
	AppBaseURL          string // APP_BASE_URL, default "http://localhost:3000", trailing "/" trimmed
	PublicSignupEnabled bool   // PUBLIC_SIGNUP_ENABLED, default false; only "true" (case-insensitive) = true
	SuperAdminEmail     string // SUPER_ADMIN_EMAIL, optional, trimmed + lower-cased
	GoogleClientID      string // GOOGLE_CLIENT_ID, optional
	CookieSecure        bool   // COOKIE_SECURE, default true; only "false" = false
}

// Load reads configuration from environment variables.
// Returns an error if DATABASE_URL is not set.
func Load() (Config, error) {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}

	appBaseURL := os.Getenv("APP_BASE_URL")
	if appBaseURL == "" {
		appBaseURL = "http://localhost:3000"
	}
	appBaseURL = strings.TrimRight(appBaseURL, "/")

	publicSignupEnabled := strings.EqualFold(strings.TrimSpace(os.Getenv("PUBLIC_SIGNUP_ENABLED")), "true")

	superAdminEmail := strings.ToLower(strings.TrimSpace(os.Getenv("SUPER_ADMIN_EMAIL")))

	googleClientID := os.Getenv("GOOGLE_CLIENT_ID")

	cookieSecure := true
	if strings.EqualFold(strings.TrimSpace(os.Getenv("COOKIE_SECURE")), "false") {
		cookieSecure = false
	}

	return Config{
		Port:                port,
		DatabaseURL:         databaseURL,
		AppBaseURL:          appBaseURL,
		PublicSignupEnabled: publicSignupEnabled,
		SuperAdminEmail:     superAdminEmail,
		GoogleClientID:      googleClientID,
		CookieSecure:        cookieSecure,
	}, nil
}
