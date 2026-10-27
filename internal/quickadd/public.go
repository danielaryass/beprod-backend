package quickadd

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"beprod/internal/app"
	"beprod/internal/httpx"
	"beprod/internal/jakarta"
	"beprod/internal/models"
	"beprod/internal/secure"
	"beprod/internal/transactions"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	qaCookie      = "beprod_qa"
	qaCookiePath  = "/api/v1/q"
	qaSessionTTL  = 30 * time.Minute
	maxFailed     = 5
	lockDuration  = 15 * time.Minute
	maxEntries    = 30
	entriesWindow = 10 * time.Minute
)

var (
	dummyOnce sync.Once
	dummyHash string
)

// getDummyHash menghitung hash sekali saja (lazy) supaya mengikuti bcrypt cost test.
func getDummyHash() string {
	dummyOnce.Do(func() { dummyHash, _ = secure.HashPassword("0000") })
	return dummyHash
}

var errLinkInactive = httpx.NewError(404, "LINK_INACTIVE", "This link is no longer active.")
var errNoSession = httpx.NewError(401, "QA_SESSION_REQUIRED", "Enter your PIN")

// writeErr menulis {"error": {"code","message", ...extra}} dan mengembalikan nil (response sudah ditulis).
func writeErr(c *fiber.Ctx, status int, code, message string, extra fiber.Map) error {
	e := fiber.Map{"code": code, "message": message}
	for k, v := range extra {
		e[k] = v
	}
	return c.Status(status).JSON(fiber.Map{"error": e})
}

// lockedErr: sisa menit dibulatkan ke atas, minimum 1.
func lockedErr(c *fiber.Ctx, until time.Time) error {
	n := int(math.Ceil(until.Sub(jakarta.Now()).Minutes()))
	if n < 1 {
		n = 1
	}
	return writeErr(c, 423, "LINK_LOCKED", fmt.Sprintf("Too many attempts. Try again in %d minutes.", n), fiber.Map{"retryAfterMinutes": n})
}

// resolve mencari link aktif dari token di URL. Tidak ketemu / disabled / pemilik bukan active -> errLinkInactive.
func resolve(db *gorm.DB, token string) (models.QuickAddLink, error) {
	var link models.QuickAddLink
	err := db.Joins("JOIN users ON users.id = quick_add_links.user_id").
		Where("quick_add_links.token_hash = ? AND quick_add_links.enabled = ? AND users.status = ?",
			secure.HashToken(token), true, models.UserActive).
		First(&link).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return link, errLinkInactive
	}
	return link, err
}

func registerPublic(api fiber.Router, d app.Deps) {
	g := api.Group("/q/:token")
	g.Get("/", getStatusPublic(d))
	g.Post("/pin", submitPIN(d))
	g.Get("/form", getForm(d))
	g.Post("/expenses", saveExpense(d))
}

// validSession mengembalikan sesi jika cookie beprod_qa valid untuk link ini; selain itu (nil, nil).
func validSession(db *gorm.DB, c *fiber.Ctx, linkID uuid.UUID) (*models.QuickAddSession, error) {
	ck := c.Cookies(qaCookie)
	if ck == "" {
		return nil, nil
	}
	var s models.QuickAddSession
	err := db.Where("token_hash = ? AND link_id = ? AND expires_at > ?", secure.HashToken(ck), linkID, jakarta.Now()).First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func setQACookie(c *fiber.Ctx, d app.Deps, token string) {
	c.Cookie(&fiber.Cookie{Name: qaCookie, Value: token, Path: qaCookiePath, MaxAge: int(qaSessionTTL.Seconds()),
		HTTPOnly: true, Secure: d.Config.CookieSecure, SameSite: "Lax"})
}

func getStatusPublic(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		link, err := resolve(d.DB, c.Params("token"))
		if err != nil {
			return err
		}
		if link.LockedUntil != nil && link.LockedUntil.After(jakarta.Now()) {
			return lockedErr(c, *link.LockedUntil)
		}
		s, err := validSession(d.DB, c, link.ID)
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"status": "ready", "sessionActive": s != nil})
	}
}

func submitPIN(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var b struct {
			Pin string `json:"pin"`
		}
		if err := httpx.Bind(c, &b); err != nil {
			return err
		}
		link, err := resolve(d.DB, c.Params("token"))
		if err != nil {
			if errors.Is(err, errLinkInactive) {
				secure.CheckPassword(getDummyHash(), b.Pin)
			}
			return err
		}
		if link.LockedUntil != nil && link.LockedUntil.After(jakarta.Now()) {
			return lockedErr(c, *link.LockedUntil)
		}

		var out struct {
			kind         string
			attemptsLeft int
			lockedUntil  time.Time
			qaToken      string
		}
		err = d.DB.Transaction(func(tx *gorm.DB) error {
			var l models.QuickAddLink
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&l, "id = ?", link.ID).Error; err != nil {
				return err
			}
			now := jakarta.Now()
			if !l.Enabled {
				out.kind = "inactive"
				return nil
			}
			if l.LockedUntil != nil && l.LockedUntil.After(now) {
				out.kind, out.lockedUntil = "locked", *l.LockedUntil
				return nil
			}
			if secure.CheckPassword(l.PinHash, b.Pin) {
				if err := tx.Model(&l).Updates(map[string]any{"failed_attempts": 0}).Error; err != nil {
					return err
				}
				tx.Where("link_id = ? AND expires_at < ?", l.ID, now).Delete(&models.QuickAddSession{})
				plain, hash := secure.NewToken()
				if err := tx.Create(&models.QuickAddSession{LinkID: l.ID, TokenHash: hash, ExpiresAt: now.Add(qaSessionTTL)}).Error; err != nil {
					return err
				}
				out.kind, out.qaToken = "ok", plain
				return nil
			}
			failed := l.FailedAttempts + 1
			if failed >= maxFailed {
				until := now.Add(lockDuration)
				if err := tx.Model(&l).Updates(map[string]any{"failed_attempts": 0, "locked_until": until}).Error; err != nil {
					return err
				}
				if err := tx.Create(&models.QuickAddLockEvent{LinkID: l.ID, LockedAt: now}).Error; err != nil {
					return err
				}
				out.kind, out.lockedUntil = "locked", until
				return nil
			}
			if err := tx.Model(&l).Updates(map[string]any{"failed_attempts": failed}).Error; err != nil {
				return err
			}
			out.kind, out.attemptsLeft = "wrong", maxFailed-failed
			return nil
		})
		if err != nil {
			return err
		}
		switch out.kind {
		case "inactive":
			return errLinkInactive
		case "locked":
			return lockedErr(c, out.lockedUntil)
		case "wrong":
			return writeErr(c, 401, "WRONG_PIN", fmt.Sprintf("Wrong PIN (%d attempts left)", out.attemptsLeft), fiber.Map{"attemptsLeft": out.attemptsLeft})
		}
		setQACookie(c, d, out.qaToken)
		return c.JSON(fiber.Map{"status": "ok"})
	}
}

func getForm(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		link, err := resolve(d.DB, c.Params("token"))
		if err != nil {
			return err
		}
		s, err := validSession(d.DB, c, link.ID)
		if err != nil {
			return err
		}
		if s == nil {
			return errNoSession
		}
		var accRows []models.Account
		if err := d.DB.Where("user_id = ? AND status = ?", link.UserID, models.StatusActive).
			Order("sort_order ASC, created_at ASC").Find(&accRows).Error; err != nil {
			return err
		}
		accs := make([]fiber.Map, 0, len(accRows))
		var defID *uuid.UUID
		for i, a := range accRows {
			accs = append(accs, fiber.Map{"id": a.ID, "name": a.Name, "type": a.Type})
			if i == 0 {
				id := a.ID
				defID = &id
			}
		}
		if link.LastAccountID != nil {
			for _, a := range accRows {
				if a.ID == *link.LastAccountID {
					id := a.ID
					defID = &id
				}
			}
		}
		cats := []struct {
			ID    uuid.UUID `json:"id"`
			Name  string    `json:"name"`
			Icon  string    `json:"icon"`
			Color string    `json:"color"`
		}{}
		if err := d.DB.Raw(`
SELECT c.id, c.name, c.icon, c.color
FROM categories c
LEFT JOIN (
    SELECT category_id, MAX(occurred_at) AS last_used
    FROM transactions
    WHERE user_id = ? AND type = 'expense' AND category_id IS NOT NULL
    GROUP BY category_id
) l ON l.category_id = c.id
WHERE c.user_id = ? AND c.type = 'expense' AND c.status = 'active'
ORDER BY l.last_used DESC NULLS LAST, c.sort_order ASC`, link.UserID, link.UserID).Scan(&cats).Error; err != nil {
			return err
		}
		return c.JSON(fiber.Map{
			"accounts":         accs,
			"categories":       cats,
			"defaultAccountId": defID,
			"setupRequired":    len(accs) == 0 || len(cats) == 0,
		})
	}
}

// existingEntry membalas ulang transaksi dengan idempotency key yang sama.
func existingEntry(d app.Deps, c *fiber.Ctx, userID uuid.UUID, key string) (bool, error) {
	var tx models.Transaction
	err := d.DB.Where("user_id = ? AND idempotency_key = ?", userID, key).First(&tx).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var name string
	if tx.CategoryID != nil {
		if err := d.DB.Model(&models.Category{}).Where("id = ?", *tx.CategoryID).Select("name").Scan(&name).Error; err != nil {
			return false, err
		}
	}
	return true, c.Status(200).JSON(fiber.Map{"amount": tx.Amount, "categoryName": name})
}

func saveExpense(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		link, err := resolve(d.DB, c.Params("token"))
		if err != nil {
			return err
		}
		sess, err := validSession(d.DB, c, link.ID)
		if err != nil {
			return err
		}
		if sess == nil {
			return errNoSession
		}
		var b struct {
			Amount     int64  `json:"amount"`
			CategoryID string `json:"categoryId"`
			AccountID  string `json:"accountId"`
			Note       string `json:"note"`
		}
		if err := httpx.Bind(c, &b); err != nil {
			return err
		}
		key := c.Get("Idempotency-Key")
		if len(key) > 64 {
			return httpx.NewError(400, "INVALID_IDEMPOTENCY_KEY", "Idempotency-Key is too long")
		}
		if key != "" {
			if done, err := existingEntry(d, c, link.UserID, key); done || err != nil {
				return err
			}
		}
		var n int64
		if err := d.DB.Model(&models.Transaction{}).
			Where("user_id = ? AND source = ? AND created_at > ?", link.UserID, models.SourceQuickAdd, jakarta.Now().Add(-entriesWindow)).
			Count(&n).Error; err != nil {
			return err
		}
		if n >= maxEntries {
			return httpx.NewError(429, "TOO_MANY_ENTRIES", "Too many entries. Please wait a moment.")
		}
		r, fields, err := transactions.ValidateFields(d.DB, link.UserID, transactions.Fields{
			Type: models.TypeExpense, Amount: b.Amount, AccountID: b.AccountID, CategoryID: b.CategoryID, Note: b.Note,
		}, nil)
		if err != nil {
			return err
		}
		if len(fields) > 0 {
			return httpx.Validation(fields)
		}
		var idem *string
		if key != "" {
			idem = &key
		}
		now := jakarta.Now()
		err = d.DB.Transaction(func(tx *gorm.DB) error {
			if _, err := transactions.Insert(tx, link.UserID, r, now, models.SourceQuickAdd, nil, idem); err != nil {
				return err
			}
			if err := tx.Model(&models.QuickAddLink{}).Where("id = ?", link.ID).Update("last_account_id", r.AccountID).Error; err != nil {
				return err
			}
			return tx.Model(&models.QuickAddSession{}).Where("id = ?", sess.ID).Update("expires_at", now.Add(qaSessionTTL)).Error
		})
		if err != nil {
			if key != "" && errors.Is(err, gorm.ErrDuplicatedKey) {
				if done, err2 := existingEntry(d, c, link.UserID, key); done || err2 != nil {
					return err2
				}
			}
			return err
		}
		setQACookie(c, d, c.Cookies(qaCookie))
		var name string
		if err := d.DB.Model(&models.Category{}).Where("id = ?", *r.CategoryID).Select("name").Scan(&name).Error; err != nil {
			return err
		}
		return c.Status(201).JSON(fiber.Map{"amount": r.Amount, "categoryName": name})
	}
}
