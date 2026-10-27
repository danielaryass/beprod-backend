package auth

import (
	"errors"
	"sync"
	"time"

	"beprod/internal/app"
	"beprod/internal/httpx"
	"beprod/internal/jakarta"
	"beprod/internal/models"
	"beprod/internal/secure"
	"beprod/internal/session"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

const (
	maxLoginFailures   = 10
	loginFailureWindow = 15 * time.Minute
)

var (
	dummyOnce sync.Once
	dummyHash string
)

// dummyPasswordHash is computed lazily so it respects the (test) bcrypt cost.
func dummyPasswordHash() string {
	dummyOnce.Do(func() { dummyHash, _ = secure.HashPassword("dummy-password-never-matches") })
	return dummyHash
}

func login(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var req struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := httpx.Bind(c, &req); err != nil {
			return err
		}
		email := NormalizeEmail(req.Email)
		fields := map[string]string{}
		if m := ValidateEmail(email); m != "" {
			fields["email"] = m
		}
		if req.Password == "" {
			fields["password"] = "Password is required"
		}
		if len(fields) > 0 {
			return httpx.Validation(fields)
		}

		now := jakarta.Now()
		var n int64
		if err := d.DB.Model(&models.LoginFailure{}).
			Where("email = ? AND created_at > ?", email, now.Add(-loginFailureWindow)).
			Count(&n).Error; err != nil {
			return err
		}
		if n >= maxLoginFailures {
			return httpx.NewError(429, "TOO_MANY_ATTEMPTS", "Too many attempts. Try again in 15 minutes.")
		}

		var u models.User
		err := d.DB.Where("email = ?", email).First(&u).Error
		found := err == nil
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		ok := false
		if found && u.PasswordHash != nil {
			ok = secure.CheckPassword(*u.PasswordHash, req.Password)
		} else {
			secure.CheckPassword(dummyPasswordHash(), req.Password)
		}
		if !ok {
			if err := d.DB.Create(&models.LoginFailure{Email: email}).Error; err != nil {
				return err
			}
			return httpx.NewError(401, "INVALID_CREDENTIALS", "Incorrect email or password")
		}

		switch u.Status {
		case models.UserActive:
		case models.UserPendingVerification:
			return httpx.NewError(403, "EMAIL_NOT_VERIFIED", "Please verify your email")
		case models.UserInvited:
			return httpx.NewError(403, "INVITE_PENDING", "Please use your invite link to set a password")
		case models.UserDeactivated:
			return httpx.NewError(403, "ACCOUNT_DEACTIVATED", "Your account has been deactivated. Contact the administrator.")
		default:
			return httpx.NewError(401, "INVALID_CREDENTIALS", "Incorrect email or password")
		}

		var token string
		err = d.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&models.User{}).Where("id = ?", u.ID).Update("last_login_at", now).Error; err != nil {
				return err
			}
			if err := tx.Where("email = ?", email).Delete(&models.LoginFailure{}).Error; err != nil {
				return err
			}
			var err error
			token, err = session.Create(tx, u.ID)
			return err
		})
		if err != nil {
			return err
		}
		session.SetCookie(c, token, d.Config.CookieSecure)
		u.LastLoginAt = &now
		return c.JSON(fiber.Map{"user": ToMeDTO(u)})
	}
}

func logout(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if err := d.DB.Where("id = ?", session.SessionID(c)).Delete(&models.Session{}).Error; err != nil {
			return err
		}
		session.ClearCookie(c, d.Config.CookieSecure)
		return c.SendStatus(204)
	}
}
