package session

import (
	"errors"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"beprod/internal/app"
	"beprod/internal/httpx"
	"beprod/internal/jakarta"
	"beprod/internal/models"
	"beprod/internal/secure"
)

const CookieName = "beprod_session"
const InactivityTTL = 30 * 24 * time.Hour

var (
	limiterOnce    sync.Once
	limiterHandler fiber.Handler
)

// rateLimit mengembalikan SATU limiter yang dipakai bersama semua group (dibuat lazy, sekali).
// ponytail: in-memory limiter, single instance; move to Redis storage if we run >1 instance
func rateLimit() fiber.Handler {
	limiterOnce.Do(func() {
		limiterHandler = limiter.New(limiter.Config{
			Max:        120,
			Expiration: time.Minute,
			KeyGenerator: func(c *fiber.Ctx) string {
				return User(c).ID.String()
			},
			LimitReached: func(c *fiber.Ctx) error {
				return httpx.NewError(fiber.StatusTooManyRequests, "TOO_MANY_REQUESTS", "Too many requests. Slow down a little.")
			},
		})
	})
	return limiterHandler
}

// Create inserts a new session for userID and returns the plain (unhashed) token.
func Create(db *gorm.DB, userID uuid.UUID) (string, error) {
	plain, hash := secure.NewToken()
	s := models.Session{
		UserID:     userID,
		TokenHash:  hash,
		LastSeenAt: jakarta.Now(),
	}
	if err := db.Create(&s).Error; err != nil {
		return "", err
	}
	return plain, nil
}

// SetCookie sets the session cookie on the response.
func SetCookie(c *fiber.Ctx, token string, isSecure bool) {
	c.Cookie(&fiber.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(InactivityTTL / time.Second),
		HTTPOnly: true,
		Secure:   isSecure,
		SameSite: "Lax",
	})
}

// ClearCookie clears the session cookie.
func ClearCookie(c *fiber.Ctx, isSecure bool) {
	c.Cookie(&fiber.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HTTPOnly: true,
		Secure:   isSecure,
		SameSite: "Lax",
	})
}

// RevokeAll deletes all sessions for the given userID.
func RevokeAll(db *gorm.DB, userID uuid.UUID) error {
	return db.Where("user_id = ?", userID).Delete(&models.Session{}).Error
}

// Require is a Fiber middleware that enforces authentication via session cookie.
func Require(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		token := c.Cookies(CookieName)
		if token == "" {
			return httpx.NewError(401, "UNAUTHENTICATED", "Please log in")
		}

		var s models.Session
		err := d.DB.Where("token_hash = ?", secure.HashToken(token)).First(&s).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return httpx.NewError(401, "UNAUTHENTICATED", "Please log in")
			}
			return err
		}

		now := jakarta.Now()
		if now.Sub(s.LastSeenAt) > InactivityTTL {
			d.DB.Delete(&models.Session{}, "id = ?", s.ID)
			return httpx.NewError(401, "UNAUTHENTICATED", "Please log in")
		}

		var u models.User
		err = d.DB.First(&u, "id = ?", s.UserID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return httpx.NewError(401, "UNAUTHENTICATED", "Please log in")
			}
			return err
		}
		if u.Status != models.UserActive {
			return httpx.NewError(401, "UNAUTHENTICATED", "Please log in")
		}

		if now.Sub(s.LastSeenAt) > time.Minute {
			d.DB.Model(&models.Session{}).Where("id = ?", s.ID).Update("last_seen_at", now)
			SetCookie(c, token, d.Config.CookieSecure)
		}

		c.Locals("user", &u)
		c.Locals("sessionID", s.ID)
		return rateLimit()(c)
	}
}

// User returns the authenticated user from the context locals.
func User(c *fiber.Ctx) *models.User {
	return c.Locals("user").(*models.User)
}

// SessionID returns the current session ID from context locals.
func SessionID(c *fiber.Ctx) uuid.UUID {
	return c.Locals("sessionID").(uuid.UUID)
}
