package auth

import (
	"errors"
	"strings"

	"beprod/internal/app"
	"beprod/internal/httpx"
	"beprod/internal/jakarta"
	"beprod/internal/models"
	"beprod/internal/secure"
	"beprod/internal/session"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

func acceptInvite(d app.Deps) fiber.Handler {
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
		expired := httpx.NewError(400, "TOKEN_EXPIRED", "This link has expired. Ask the administrator for a new invite.")
		tok, err := FindToken(d.DB, strings.TrimSpace(body.Token), models.TokenInvite)
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
		if u.Status == models.UserDeactivated {
			return httpx.NewError(403, "ACCOUNT_DEACTIVATED", "Your account has been deactivated. Contact the administrator.")
		}
		if u.Status != models.UserInvited {
			return expired
		}
		hash, err := secure.HashPassword(body.Password)
		if err != nil {
			return err
		}
		now := jakarta.Now()
		err = d.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&models.User{}).Where("id = ?", u.ID).Updates(map[string]any{"password_hash": hash, "status": models.UserActive, "last_login_at": now}).Error; err != nil {
				return err
			}
			return MarkUsed(tx, tok.ID)
		})
		if err != nil {
			return err
		}
		u.PasswordHash = &hash
		u.Status = models.UserActive
		u.LastLoginAt = &now
		sess, err := session.Create(d.DB, u.ID)
		if err != nil {
			return err
		}
		session.SetCookie(c, sess, d.Config.CookieSecure)
		return c.JSON(fiber.Map{"user": ToMeDTO(u)})
	}
}
