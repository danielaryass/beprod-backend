package auth

import (
	"errors"
	"strings"
	"time"

	"beprod/internal/app"
	"beprod/internal/httpx"
	"beprod/internal/jakarta"
	"beprod/internal/mail"
	"beprod/internal/models"
	"beprod/internal/secure"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

const verifySubject = "Verify your BeProd email"

func signup(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if !d.Config.PublicSignupEnabled {
			return httpx.NewError(403, "SIGNUP_CLOSED", "BeProd is currently invite-only")
		}
		var body struct {
			Name     string `json:"name"`
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := httpx.Bind(c, &body); err != nil {
			return err
		}
		name := strings.TrimSpace(body.Name)
		email := NormalizeEmail(body.Email)
		fields := map[string]string{}
		if m := ValidateName(name); m != "" {
			fields["name"] = m
		}
		if m := ValidateEmail(email); m != "" {
			fields["email"] = m
		}
		if m := ValidatePassword(body.Password); m != "" {
			fields["password"] = m
		}
		if len(fields) > 0 {
			return httpx.Validation(fields)
		}
		hash, err := secure.HashPassword(body.Password)
		if err != nil {
			return err
		}
		var plain string
		err = d.DB.Transaction(func(tx *gorm.DB) error {
			user, err := CreateUser(tx, NewUser{Name: name, Email: email, PasswordHash: &hash, Status: models.UserPendingVerification})
			if err != nil {
				return err
			}
			plain, err = IssueToken(tx, user.ID, models.TokenVerifyEmail, 24*time.Hour)
			return err
		})
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return httpx.NewError(409, "EMAIL_TAKEN", "An account with this email already exists. Log in instead.")
		}
		if err != nil {
			return err
		}
		sendMail(d, mail.Message{To: email, Subject: verifySubject, Body: "Hi " + name + ",\n\nVerify your email to finish signing up (link valid for 24 hours):\n" + tokenLink(d.Config, "/verify-email", plain) + "\n"})
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"message": "Check your inbox to verify your email."})
	}
}

func verifyEmail(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var body struct {
			Token string `json:"token"`
		}
		if err := httpx.Bind(c, &body); err != nil {
			return err
		}
		invalid := httpx.NewError(400, "TOKEN_INVALID", "This link is invalid.")
		token := strings.TrimSpace(body.Token)
		if token == "" {
			return invalid
		}
		tok, err := FindToken(d.DB, token, models.TokenVerifyEmail)
		var u models.User
		switch {
		case errors.Is(err, ErrTokenInvalid):
			return invalid
		case errors.Is(err, ErrTokenExpired):
			if err := d.DB.First(&u, "id = ?", tok.UserID).Error; err != nil {
				return err
			}
			if tok.UsedAt != nil && u.Status == models.UserActive {
				return c.JSON(fiber.Map{"message": "Email already verified. Please log in.", "alreadyVerified": true})
			}
			return httpx.NewError(400, "TOKEN_EXPIRED", "This link has expired. Request a new one.")
		case err != nil:
			return err
		}
		if err := d.DB.First(&u, "id = ?", tok.UserID).Error; err != nil {
			return err
		}
		if u.Status == models.UserDeactivated {
			return httpx.NewError(403, "ACCOUNT_DEACTIVATED", "Your account has been deactivated. Contact the administrator.")
		}
		err = d.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&models.User{}).Where("id = ? AND status = ?", u.ID, models.UserPendingVerification).
				Update("status", models.UserActive).Error; err != nil {
				return err
			}
			return MarkUsed(tx, tok.ID)
		})
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"message": "Email verified. Please log in.", "alreadyVerified": false})
	}
}

func resendVerification(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var body struct {
			Email string `json:"email"`
		}
		if err := httpx.Bind(c, &body); err != nil {
			return err
		}
		ok := fiber.Map{"message": "If this email needs verification, a new link has been sent."}
		var u models.User
		err := d.DB.Where("email = ?", NormalizeEmail(body.Email)).First(&u).Error
		if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && u.Status != models.UserPendingVerification) {
			return c.JSON(ok)
		}
		if err != nil {
			return err
		}
		var last models.UserToken
		err = d.DB.Where("user_id = ? AND purpose = ?", u.ID, models.TokenVerifyEmail).Order("created_at DESC").First(&last).Error
		if err == nil && jakarta.Now().Sub(last.CreatedAt) < 60*time.Second {
			return httpx.NewError(429, "RESEND_TOO_SOON", "Please wait a minute before requesting another link.")
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		plain, err := IssueToken(d.DB, u.ID, models.TokenVerifyEmail, 24*time.Hour)
		if err != nil {
			return err
		}
		sendMail(d, mail.Message{To: u.Email, Subject: verifySubject, Body: "Verify your email (link valid for 24 hours):\n" + tokenLink(d.Config, "/verify-email", plain) + "\n"})
		return c.JSON(ok)
	}
}
