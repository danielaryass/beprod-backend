package transactions

import (
	"errors"
	"strings"
	"time"

	"beprod/internal/app"
	"beprod/internal/httpx"
	"beprod/internal/jakarta"
	"beprod/internal/models"
	"beprod/internal/session"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func Register(api fiber.Router, d app.Deps) {
	g := api.Group("/transactions", session.Require(d))
	g.Get("/", listHandler(d))
	g.Post("/", createHandler(d))
	g.Get("/:id", getHandler(d))
	g.Put("/:id", updateHandler(d))
	g.Delete("/:id", deleteHandler(d))
	api.Get("/tags", session.Require(d), tagsHandler(d))
}

type writeRequest struct {
	Fields
	Date string `json:"date"`
	Time string `json:"time"`
}

// parseWhen validates date/time into the fields map and returns the instant (zero if invalid).
func parseWhen(date, hhmm string, fields map[string]string) time.Time {
	var day time.Time
	dateOK := false
	if date == "" {
		fields["date"] = "Select a date"
	} else if p, err := jakarta.ParseDate(date); err != nil {
		fields["date"] = "Enter a valid date"
	} else if p.After(jakarta.Today()) {
		fields["date"] = "Date can't be in the future"
	} else {
		day, dateOK = p, true
	}
	// validated against today so a time error shows even when the date is wrong
	if _, err := jakarta.Combine(jakarta.Today(), hhmm); err != nil {
		fields["time"] = "Enter a valid time (HH:MM)"
		return time.Time{}
	}
	if !dateOK {
		return time.Time{}
	}
	at, _ := jakarta.Combine(day, hhmm)
	return at
}

func toDTO(db *gorm.DB, userID uuid.UUID, t models.Transaction) (DTO, error) {
	dtos, err := ToDTOs(db, userID, []models.Transaction{t})
	if err != nil {
		return DTO{}, err
	}
	return dtos[0], nil
}

func loadOwned(db *gorm.DB, userID, id uuid.UUID) (models.Transaction, error) {
	var rows []models.Transaction
	if err := db.Where("id = ? AND user_id = ?", id, userID).Limit(1).Find(&rows).Error; err != nil {
		return models.Transaction{}, err
	}
	if len(rows) == 0 {
		return models.Transaction{}, httpx.NotFound()
	}
	return rows[0], nil
}

func resolvedOf(t models.Transaction) Resolved {
	return Resolved{Type: t.Type, Amount: t.Amount, AccountID: t.AccountID, ToAccountID: t.ToAccountID,
		CategoryID: t.CategoryID, Note: t.Note, Tags: t.Tags}
}

func respond(c *fiber.Ctx, d app.Deps, userID uuid.UUID, status int, t models.Transaction) error {
	dto, err := toDTO(d.DB, userID, t)
	if err != nil {
		return err
	}
	return c.Status(status).JSON(fiber.Map{"transaction": dto})
}

// byKey returns the user's transaction with this idempotency key, found=false if none.
func byKey(db *gorm.DB, userID uuid.UUID, key string) (models.Transaction, bool, error) {
	var rows []models.Transaction
	err := db.Where("user_id = ? AND idempotency_key = ?", userID, key).Limit(1).Find(&rows).Error
	if err != nil || len(rows) == 0 {
		return models.Transaction{}, false, err
	}
	return rows[0], true, nil
}

func createHandler(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		key := strings.TrimSpace(c.Get("Idempotency-Key"))
		if len(key) > 64 {
			return httpx.NewError(400, "INVALID_IDEMPOTENCY_KEY", "Idempotency-Key is too long")
		}
		var req writeRequest
		if err := httpx.Bind(c, &req); err != nil {
			return err
		}
		var idem *string
		if key != "" {
			idem = &key
			existing, found, err := byKey(d.DB, u.ID, key)
			if err != nil {
				return err
			}
			if found {
				return respond(c, d, u.ID, 200, existing)
			}
		}
		r, fields, err := ValidateFields(d.DB, u.ID, req.Fields, nil)
		if err != nil {
			return err
		}
		occurredAt := parseWhen(req.Date, req.Time, fields)
		if len(fields) > 0 {
			return httpx.Validation(fields)
		}
		t, err := Insert(d.DB, u.ID, r, occurredAt, models.SourceApp, nil, idem)
		if errors.Is(err, gorm.ErrDuplicatedKey) && key != "" {
			existing, found, qerr := byKey(d.DB, u.ID, key)
			if qerr != nil {
				return qerr
			}
			if found {
				return respond(c, d, u.ID, 200, existing)
			}
		}
		if err != nil {
			return err
		}
		return respond(c, d, u.ID, 201, t)
	}
}

func getHandler(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		t, err := loadOwned(d.DB, u.ID, id)
		if err != nil {
			return err
		}
		return respond(c, d, u.ID, 200, t)
	}
}

func updateHandler(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		t, err := loadOwned(d.DB, u.ID, id)
		if err != nil {
			return err
		}
		if t.Type == models.TypeAdjustment {
			return httpx.NewError(400, "ADJUSTMENT_READONLY", "Balance adjustments can't be edited. Delete it and adjust again.")
		}
		var req writeRequest
		if err := httpx.Bind(c, &req); err != nil {
			return err
		}
		keep := resolvedOf(t)
		r, fields, err := ValidateFields(d.DB, u.ID, req.Fields, &keep)
		if err != nil {
			return err
		}
		occurredAt := parseWhen(req.Date, req.Time, fields)
		if len(fields) > 0 {
			return httpx.Validation(fields)
		}
		t.Type, t.Amount, t.OccurredAt = r.Type, r.Amount, occurredAt
		t.AccountID, t.ToAccountID, t.CategoryID = r.AccountID, r.ToAccountID, r.CategoryID
		t.Note, t.Tags = r.Note, r.Tags
		if err := d.DB.Save(&t).Error; err != nil {
			return err
		}
		return respond(c, d, u.ID, 200, t)
	}
}

func deleteHandler(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		res := d.DB.Where("id = ? AND user_id = ?", id, u.ID).Delete(&models.Transaction{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return httpx.NotFound()
		}
		return c.SendStatus(204)
	}
}
