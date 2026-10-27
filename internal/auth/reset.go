package auth

import (
	"errors"
	"strings"
	"time"

	"beprod/internal/app"
	"beprod/internal/httpx"
	"beprod/internal/mail"
	"beprod/internal/models"
	"beprod/internal/secure"
	"beprod/internal/session"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

func forgotPassword(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var body struct {
			Email string `json:"email"`
		}
		if err := httpx.Bind(c, &body); err != nil {
			return err
		}
		var u models.User
		err := d.DB.Where("email = ?", NormalizeEmail(body.Email)).First(&u).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && u.Status == models.UserActive {
			plain, err := IssueToken(d.DB, u.ID, models.TokenResetPassword, 60*time.Minute)
			if err != nil {
				return err
			}
			link := tokenLink(d.Config, "/reset-password", plain)
			sendMail(d, mail.Message{
				To:      u.Email,
				Subject: "Reset your BeProd password",
				Body:    "Hi " + u.Name + ",\n\nUse this link to set a new password (valid for 60 minutes):\n" + link + "\n\nIf you didn't ask for this, ignore this email.\n",
			})
		}
		return c.JSON(fiber.Map{"message": "If this email is registered, a reset link has been sent"})
	}
}

func resetPassword(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var body struct {
			Token    string `json:"token"`
			Password string `json:"password"`
		}
		if err := httpx.Bind(c, &body); err != nil {
			return err
		}
		if msg := ValidatePassword(body.Password); msg != "" {
			return httpx.Validation(map[string]string{"password": msg})
		}
		expired := httpx.NewError(400, "TOKEN_EXPIRED", "This link has expired. Request a new one.")
		tok, err := FindToken(d.DB, strings.TrimSpace(body.Token), models.TokenResetPassword)
		if errors.Is(err, ErrTokenInvalid) || errors.Is(err, ErrTokenExpired) {
			return expired
		}
		if err != nil {
			return err
		}
		var u models.User
		if err := d.DB.First(&u, "id = ?", tok.UserID).Error; err != nil {
			return err
		}
		if u.Status != models.UserActive {
			return expired
		}
		hash, err := secure.HashPassword(body.Password)
		if err != nil {
			return err
		}
		err = d.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&models.User{}).Where("id = ?", u.ID).Update("password_hash", hash).Error; err != nil {
				return err
			}
			if err := MarkUsed(tx, tok.ID); err != nil {
				return err
			}
			return session.RevokeAll(tx, u.ID)
		})
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"message": "Password updated. Please log in."})
	}
}
