package app

import (
	"context"

	"beprod/internal/config"
	"beprod/internal/mail"

	"gorm.io/gorm"
)

// GoogleClaims holds verified claims from a Google ID token.
type GoogleClaims struct {
	Subject       string
	Email         string
	Name          string
	EmailVerified bool
}

// GoogleVerifier is a function that verifies a Google ID token.
type GoogleVerifier func(ctx context.Context, idToken string) (GoogleClaims, error)

// Deps holds the application-wide dependencies.
type Deps struct {
	DB           *gorm.DB
	Config       config.Config
	Mailer       mail.Mailer
	VerifyGoogle GoogleVerifier
}
