package goals

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"beprod/internal/accounts"
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
	g := api.Group("/goals", session.Require(d))
	g.Get("/", list(d))
	g.Post("/", create(d))
	g.Patch("/:id", update(d))
	g.Post("/:id/complete", complete(d))
	g.Delete("/:id", remove(d))
}

// DTO is the Goal object of the API contract.
type DTO struct {
	ID               uuid.UUID  `json:"id"`
	Name             string     `json:"name"`
	TargetAmount     int64      `json:"targetAmount"`
	TargetDate       *string    `json:"targetDate"`
	AccountID        uuid.UUID  `json:"accountId"`
	AccountName      string     `json:"accountName"`
	Status           string     `json:"status"`
	Saved            int64      `json:"saved"`
	Percent          int64      `json:"percent"`
	Monthly          *int64     `json:"monthly"`
	UnsavedThisMonth int64      `json:"unsavedThisMonth"`
	Reached          bool       `json:"reached"`
	CompletedAt      *time.Time `json:"completedAt"`
}

// toDTOs adds progress (from each goal's account balance) to the goals.
func toDTOs(db *gorm.DB, userID uuid.UUID, gs []models.Goal, today time.Time) ([]DTO, error) {
	out := []DTO{}
	if len(gs) == 0 {
		return out, nil
	}
	var accs []models.Account
	if err := db.Where("user_id = ?", userID).Find(&accs).Error; err != nil {
		return nil, err
	}
	byID := make(map[uuid.UUID]models.Account, len(accs))
	for _, a := range accs {
		byID[a.ID] = a
	}
	bal, err := accounts.Balances(db, userID)
	if err != nil {
		return nil, err
	}
	prev := jakarta.MonthStart(today).AddDate(0, 0, -1)
	for _, g := range gs {
		acc := byID[g.AccountID]
		start, err := accounts.BalanceAt(db, acc, prev)
		if err != nil {
			return nil, err
		}
		p := Calc(g.TargetAmount, bal[g.AccountID], start, today, g.TargetDate)
		dto := DTO{ID: g.ID, Name: g.Name, TargetAmount: g.TargetAmount, AccountID: g.AccountID, AccountName: acc.Name,
			Status: g.Status, Saved: p.Saved, Percent: p.Percent, Monthly: p.Monthly, UnsavedThisMonth: p.Unsaved,
			Reached: p.Reached, CompletedAt: g.CompletedAt}
		if g.TargetDate != nil {
			s := jakarta.FormatDate(*g.TargetDate)
			dto.TargetDate = &s
		}
		if g.Status == models.GoalCompleted {
			dto.UnsavedThisMonth = 0
		}
		out = append(out, dto)
	}
	return out, nil
}

// ActiveDTOs returns the user's active goals with progress, ordered by target date (nulls last), then created.
func ActiveDTOs(db *gorm.DB, userID uuid.UUID, today time.Time) ([]DTO, error) {
	var gs []models.Goal
	err := db.Where("user_id = ? AND status = ?", userID, models.GoalActive).
		Order("target_date ASC NULLS LAST, created_at ASC").Find(&gs).Error
	if err != nil {
		return nil, err
	}
	return toDTOs(db, userID, gs, today)
}

func one(db *gorm.DB, userID, id uuid.UUID) (DTO, error) {
	var g models.Goal
	if err := db.Where("id = ? AND user_id = ?", id, userID).First(&g).Error; err != nil {
		return DTO{}, err
	}
	out, err := toDTOs(db, userID, []models.Goal{g}, jakarta.Today())
	if err != nil {
		return DTO{}, err
	}
	return out[0], nil
}

func load(db *gorm.DB, userID, id uuid.UUID) (models.Goal, error) {
	var g models.Goal
	err := db.Where("id = ? AND user_id = ?", id, userID).First(&g).Error
	return g, err
}

func list(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		active, err := ActiveDTOs(d.DB, u.ID, jakarta.Today())
		if err != nil {
			return err
		}
		var done []models.Goal
		if err := d.DB.Where("user_id = ? AND status = ?", u.ID, models.GoalCompleted).
			Order("completed_at DESC").Find(&done).Error; err != nil {
			return err
		}
		completed, err := toDTOs(d.DB, u.ID, done, jakarta.Today())
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"goals": active, "completed": completed})
	}
}

type input struct {
	Name         *string         `json:"name"`
	TargetAmount *int64          `json:"targetAmount"`
	TargetDate   json.RawMessage `json:"targetDate"` // absent = keep, null = clear
	AccountID    *string         `json:"accountId"`
}

var errTaken = httpx.Validation(map[string]string{"accountId": "This account already has a goal"})

// apply validates in against g's current values (zero for create) and writes valid values into g.
func apply(tx *gorm.DB, userID uuid.UUID, g *models.Goal, in input, create bool) error {
	fields := map[string]string{}
	if in.Name != nil || create {
		name := ""
		if in.Name != nil {
			name = strings.TrimSpace(*in.Name)
		}
		if n := utf8.RuneCountInString(name); n < 1 || n > 40 {
			fields["name"] = "Name must be 1–40 characters"
		} else {
			g.Name = name
		}
	}
	if in.TargetAmount != nil || create {
		if in.TargetAmount == nil || *in.TargetAmount <= 0 {
			fields["targetAmount"] = "Enter an amount above 0"
		} else {
			g.TargetAmount = *in.TargetAmount
		}
	}
	if in.TargetDate != nil {
		if string(in.TargetDate) == "null" {
			g.TargetDate = nil
		} else {
			var s string
			date, err := time.Time{}, json.Unmarshal(in.TargetDate, &s)
			if err == nil {
				date, err = jakarta.ParseDate(s)
			}
			if err != nil || !date.After(jakarta.Today()) {
				fields["targetDate"] = "Target date must be in the future"
			} else {
				g.TargetDate = &date
			}
		}
	}
	if in.AccountID != nil || create {
		const choose = "Choose one of your accounts"
		id, err := uuid.Parse(deref(in.AccountID))
		var acc models.Account
		if err == nil {
			err = tx.Where("id = ? AND user_id = ? AND status = ?", id, userID, models.StatusActive).First(&acc).Error
		}
		switch {
		case err != nil:
			fields["accountId"] = choose
		case acc.Type == models.AccountCreditCard:
			fields["accountId"] = "Credit cards can't hold savings"
		default:
			var n int64
			if err := tx.Model(&models.Goal{}).Where("account_id = ? AND status = ? AND id <> ?", id, models.GoalActive, g.ID).
				Count(&n).Error; err != nil {
				return err
			}
			if n > 0 {
				fields["accountId"] = "This account already has a goal"
			} else {
				g.AccountID = id
			}
		}
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func create(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		var in input
		if err := httpx.Bind(c, &in); err != nil {
			return err
		}
		g := models.Goal{UserID: u.ID, Status: models.GoalActive}
		err := d.DB.Transaction(func(tx *gorm.DB) error {
			if err := apply(tx, u.ID, &g, in, true); err != nil {
				return err
			}
			return tx.Create(&g).Error
		})
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return errTaken
		}
		if err != nil {
			return err
		}
		dto, err := one(d.DB, u.ID, g.ID)
		if err != nil {
			return err
		}
		return c.Status(201).JSON(fiber.Map{"goal": dto})
	}
}

func update(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		var in input
		if err := httpx.Bind(c, &in); err != nil {
			return err
		}
		err = d.DB.Transaction(func(tx *gorm.DB) error {
			g, err := load(tx, u.ID, id)
			if err != nil {
				return err
			}
			if g.Status == models.GoalCompleted {
				return httpx.NewError(400, "GOAL_COMPLETED", "Completed goals can't be edited")
			}
			if err := apply(tx, u.ID, &g, in, false); err != nil {
				return err
			}
			return tx.Save(&g).Error
		})
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return errTaken
		}
		if err != nil {
			return err
		}
		dto, err := one(d.DB, u.ID, id)
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"goal": dto})
	}
}

func complete(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		if _, err := load(d.DB, u.ID, id); err != nil {
			return err
		}
		err = d.DB.Model(&models.Goal{}).Where("id = ? AND user_id = ? AND status = ?", id, u.ID, models.GoalActive).
			Updates(map[string]any{"status": models.GoalCompleted, "completed_at": jakarta.Now()}).Error
		if err != nil {
			return err
		}
		dto, err := one(d.DB, u.ID, id)
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"goal": dto})
	}
}

func remove(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		if _, err := load(d.DB, u.ID, id); err != nil {
			return err
		}
		if err := d.DB.Where("id = ? AND user_id = ?", id, u.ID).Delete(&models.Goal{}).Error; err != nil {
			return err
		}
		return c.SendStatus(204)
	}
}
