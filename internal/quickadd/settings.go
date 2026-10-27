package quickadd

import (
	"errors"
	"regexp"

	"beprod/internal/app"
	"beprod/internal/httpx"
	"beprod/internal/jakarta"
	"beprod/internal/models"
	"beprod/internal/secure"
	"beprod/internal/session"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var pinRe = regexp.MustCompile(`^\d{4}$`)

type pinBody struct {
	Pin        string `json:"pin"`
	PinConfirm string `json:"pinConfirm"`
}

// validatePIN mengembalikan nil jika valid. PIN tidak valid -> hanya fields.pin. Valid tapi konfirmasi beda -> fields.pinConfirm.
func validatePIN(b pinBody) *httpx.Error {
	if !pinRe.MatchString(b.Pin) {
		return httpx.Validation(map[string]string{"pin": "PIN must be exactly 4 digits"})
	}
	if b.PinConfirm != b.Pin {
		return httpx.Validation(map[string]string{"pinConfirm": "PINs don't match"})
	}
	return nil
}

func registerSettings(api fiber.Router, d app.Deps) {
	g := api.Group("/quick-add", session.Require(d))
	g.Get("/", getStatus(d))
	g.Post("/enable", enable(d))
	g.Put("/pin", changePIN(d))
	g.Post("/regenerate", regenerate(d))
	g.Post("/disable", disable(d))
}

func linkURL(d app.Deps, token string) string { return d.Config.AppBaseURL + "/q/" + token }

// findLink mengembalikan link milik user atau (nil, nil) jika belum ada.
func findLink(db *gorm.DB, userID uuid.UUID) (*models.QuickAddLink, error) {
	var l models.QuickAddLink
	err := db.Where("user_id = ?", userID).First(&l).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &l, nil
}

func errDisabled() *httpx.Error {
	return httpx.NewError(400, "QUICK_ADD_DISABLED", "Turn on Quick Add first")
}

func errEnabled() *httpx.Error {
	return httpx.NewError(409, "QUICK_ADD_ENABLED", "Quick Add is already on")
}

func getStatus(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		link, err := findLink(d.DB, u.ID)
		if err != nil {
			return err
		}
		if link == nil {
			return c.JSON(fiber.Map{"enabled": false, "lockedUntil": nil, "lockEvents": []fiber.Map{}})
		}
		var lockedUntil any
		if link.LockedUntil != nil && link.LockedUntil.After(jakarta.Now()) {
			lockedUntil = *link.LockedUntil
		}
		var events []models.QuickAddLockEvent
		if err := d.DB.Where("link_id = ?", link.ID).Order("locked_at DESC").Limit(10).Find(&events).Error; err != nil {
			return err
		}
		out := make([]fiber.Map, 0, len(events))
		for _, e := range events {
			out = append(out, fiber.Map{"lockedAt": e.LockedAt})
		}
		return c.JSON(fiber.Map{"enabled": link.Enabled, "lockedUntil": lockedUntil, "lockEvents": out})
	}
}

func enable(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		var b pinBody
		if err := httpx.Bind(c, &b); err != nil {
			return err
		}
		if e := validatePIN(b); e != nil {
			return e
		}
		pinHash, err := secure.HashPassword(b.Pin)
		if err != nil {
			return err
		}
		token, tokenHash := secure.NewToken()
		err = d.DB.Transaction(func(tx *gorm.DB) error {
			link, err := findLink(tx, u.ID)
			if err != nil {
				return err
			}
			if link != nil && link.Enabled {
				return errEnabled()
			}
			if link == nil {
				link = &models.QuickAddLink{UserID: u.ID, TokenHash: tokenHash, PinHash: pinHash, Enabled: true}
				if err := tx.Create(link).Error; err != nil {
					if errors.Is(err, gorm.ErrDuplicatedKey) {
						return errEnabled()
					}
					return err
				}
			} else if err := tx.Model(link).Updates(map[string]any{
				"token_hash": tokenHash, "pin_hash": pinHash, "enabled": true,
				"failed_attempts": 0, "locked_until": nil,
			}).Error; err != nil {
				return err
			}
			return tx.Where("link_id = ?", link.ID).Delete(&models.QuickAddSession{}).Error
		})
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"url": linkURL(d, token)})
	}
}

func changePIN(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		var b pinBody
		if err := httpx.Bind(c, &b); err != nil {
			return err
		}
		if e := validatePIN(b); e != nil {
			return e
		}
		link, err := findLink(d.DB, u.ID)
		if err != nil {
			return err
		}
		if link == nil || !link.Enabled {
			return errDisabled()
		}
		h, err := secure.HashPassword(b.Pin)
		if err != nil {
			return err
		}
		if err := d.DB.Model(link).Updates(map[string]any{"pin_hash": h, "failed_attempts": 0, "locked_until": nil}).Error; err != nil {
			return err
		}
		return c.SendStatus(204)
	}
}

func regenerate(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		link, err := findLink(d.DB, u.ID)
		if err != nil {
			return err
		}
		if link == nil || !link.Enabled {
			return errDisabled()
		}
		token, tokenHash := secure.NewToken()
		err = d.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(link).Updates(map[string]any{"token_hash": tokenHash}).Error; err != nil {
				return err
			}
			return tx.Where("link_id = ?", link.ID).Delete(&models.QuickAddSession{}).Error
		})
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"url": linkURL(d, token)})
	}
}

func disable(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		link, err := findLink(d.DB, u.ID)
		if err != nil {
			return err
		}
		if link == nil || !link.Enabled {
			return c.SendStatus(204)
		}
		err = d.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(link).Updates(map[string]any{"enabled": false}).Error; err != nil {
				return err
			}
			return tx.Where("link_id = ?", link.ID).Delete(&models.QuickAddSession{}).Error
		})
		if err != nil {
			return err
		}
		return c.SendStatus(204)
	}
}
