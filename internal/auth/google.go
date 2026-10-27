package auth

import (
	"errors"
	"strings"

	"beprod/internal/app"
	"beprod/internal/httpx"
	"beprod/internal/jakarta"
	"beprod/internal/models"
	"beprod/internal/session"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

// googleLogin handles POST /api/v1/auth/google: verify the ID token, link/activate/create the user, start a session.
func googleLogin(d app.Deps) fiber.Handler {
	invalid := httpx.NewError(401, "INVALID_GOOGLE_TOKEN", "Google sign-in failed. Try again.")
	return func(c *fiber.Ctx) error {
		var in struct {
			IDToken string `json:"idToken"`
		}
		if err := httpx.Bind(c, &in); err != nil {
			return err
		}
		idToken := strings.TrimSpace(in.IDToken)
		if idToken == "" {
			return httpx.Validation(map[string]string{"idToken": "idToken is required"})
		}
		if d.VerifyGoogle == nil {
			return invalid
		}
		claims, err := d.VerifyGoogle(c.UserContext(), idToken)
		if err != nil || claims.Subject == "" || strings.TrimSpace(claims.Email) == "" {
			return invalid
		}
		if !claims.EmailVerified {
			return httpx.NewError(401, "GOOGLE_EMAIL_NOT_VERIFIED", "Your Google email is not verified.")
		}
		email := NormalizeEmail(claims.Email)

		var user models.User
		err = d.DB.Transaction(func(tx *gorm.DB) error {
			now := jakarta.Now()
			found := true
			err := tx.Where("google_subject = ?", claims.Subject).First(&user).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				err = tx.Where("email = ?", email).First(&user).Error
				if errors.Is(err, gorm.ErrRecordNotFound) {
					found = false
					err = nil
				}
			}
			if err != nil {
				return err
			}

			if found {
				if user.Status == models.UserDeactivated {
					return httpx.NewError(403, "ACCOUNT_DEACTIVATED", "Your account has been deactivated. Contact the administrator.")
				}
				if user.GoogleSubject != nil && *user.GoogleSubject != claims.Subject {
					return httpx.NewError(409, "GOOGLE_ACCOUNT_CONFLICT", "This email is linked to a different Google account.")
				}
				updates := map[string]any{"last_login_at": now}
				if user.GoogleSubject == nil {
					updates["google_subject"] = claims.Subject
				}
				if user.Status == models.UserInvited || user.Status == models.UserPendingVerification {
					updates["status"] = models.UserActive
					if err := tx.Model(&models.UserToken{}).
						Where("user_id = ? AND purpose IN ? AND used_at IS NULL", user.ID, []string{models.TokenInvite, models.TokenVerifyEmail}).
						Update("used_at", now).Error; err != nil {
						return err
					}
				}
				if err := tx.Model(&models.User{}).Where("id = ?", user.ID).Updates(updates).Error; err != nil {
					return err
				}
				if user.GoogleSubject == nil {
					sub := claims.Subject
					user.GoogleSubject = &sub
				}
				if _, ok := updates["status"]; ok {
					user.Status = models.UserActive
				}
				user.LastLoginAt = &now
				return nil
			}

			if !d.Config.PublicSignupEnabled {
				return httpx.NewError(403, "SIGNUP_CLOSED", "BeProd is currently invite-only")
			}
			name := TruncateRunes(strings.TrimSpace(claims.Name), 60)
			if name == "" {
				name = NameFromEmail(email)
			}
			sub := claims.Subject
			user, err = CreateUser(tx, NewUser{Name: name, Email: email, GoogleSubject: &sub, Status: models.UserActive})
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return httpx.NewError(409, "EMAIL_TAKEN", "An account with this email already exists. Log in instead.")
			}
			if err != nil {
				return err
			}
			if err := tx.Model(&models.User{}).Where("id = ?", user.ID).Update("last_login_at", now).Error; err != nil {
				return err
			}
			user.LastLoginAt = &now
			return nil
		})
		if err != nil {
			return err
		}

		token, err := session.Create(d.DB, user.ID)
		if err != nil {
			return err
		}
		session.SetCookie(c, token, d.Config.CookieSecure)
		return c.Status(200).JSON(fiber.Map{"user": ToMeDTO(user)})
	}
}
