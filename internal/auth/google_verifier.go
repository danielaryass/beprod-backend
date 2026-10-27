package auth

import (
	"context"
	"errors"

	"beprod/internal/app"

	"google.golang.org/api/idtoken"
)

// NewGoogleVerifier returns the real verifier. It validates the ID token's signature, expiry and audience against Google.
// Not unit-tested against the network.
func NewGoogleVerifier(clientID string) app.GoogleVerifier {
	if clientID == "" {
		// idtoken.Validate skips the audience check when audience is "": never allow that.
		return func(ctx context.Context, idToken string) (app.GoogleClaims, error) {
			return app.GoogleClaims{}, errors.New("google sign-in is not configured")
		}
	}
	return func(ctx context.Context, idToken string) (app.GoogleClaims, error) {
		p, err := idtoken.Validate(ctx, idToken, clientID)
		if err != nil {
			return app.GoogleClaims{}, err
		}
		email, _ := p.Claims["email"].(string)
		name, _ := p.Claims["name"].(string)
		verified, _ := p.Claims["email_verified"].(bool)
		return app.GoogleClaims{Subject: p.Subject, Email: email, Name: name, EmailVerified: verified}, nil
	}
}
