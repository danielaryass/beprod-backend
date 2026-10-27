package daily

import (
	"errors"
	"strings"
	"unicode/utf8"

	"beprod/internal/app"
	"beprod/internal/httpx"
	"beprod/internal/models"
	"beprod/internal/session"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const maxActiveHabits = 30

// HabitDTO is the Habit object of the API contract.
type HabitDTO struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Kind   string    `json:"kind"`
	Target int       `json:"target"`
	Unit   string    `json:"unit"`
	Days   []int     `json:"days"`
	Hidden bool      `json:"hidden"`
}

func habitDTO(h models.Habit) HabitDTO {
	return HabitDTO{ID: h.ID, Name: h.Name, Kind: h.Kind, Target: h.Target, Unit: h.Unit,
		Days: Weekdays(h.Days).List(), Hidden: h.Hidden}
}

func habitDTOs(hs []models.Habit) []HabitDTO {
	out := make([]HabitDTO, 0, len(hs))
	for _, h := range hs {
		out = append(out, habitDTO(h))
	}
	return out
}

var errHabitTaken = httpx.Validation(map[string]string{"name": "You already have a habit with this name"})

func loadHabit(db *gorm.DB, userID, id uuid.UUID) (models.Habit, error) {
	var h models.Habit
	err := db.Where("id = ? AND user_id = ?", id, userID).First(&h).Error
	return h, err
}

func listHabits(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		var active, hidden []models.Habit
		if err := d.DB.Where("user_id = ? AND NOT hidden", u.ID).Order("sort_order ASC, created_at ASC").Find(&active).Error; err != nil {
			return err
		}
		if err := d.DB.Where("user_id = ? AND hidden", u.ID).Order("lower(name) ASC").Find(&hidden).Error; err != nil {
			return err
		}
		return c.JSON(fiber.Map{"habits": habitDTOs(active), "hidden": habitDTOs(hidden)})
	}
}

type habitInput struct {
	Name   *string `json:"name"`
	Kind   *string `json:"kind"` // create only
	Target *int    `json:"target"`
	Unit   *string `json:"unit"`
	Days   *[]int  `json:"days"`
	Hidden *bool   `json:"hidden"`
}

// activeStats returns the number and the max sort_order of the user's active habits.
func activeStats(db *gorm.DB, userID uuid.UUID) (count int64, maxOrder int, err error) {
	err = db.Model(&models.Habit{}).Where("user_id = ? AND NOT hidden", userID).
		Select("COUNT(*), COALESCE(MAX(sort_order), 0)").Row().Scan(&count, &maxOrder)
	return
}

// applyHabit validates in against h's current values (zero for create) and writes valid values into h.
func applyHabit(tx *gorm.DB, userID uuid.UUID, h *models.Habit, in habitInput, create bool) error {
	fields := map[string]string{}
	if in.Name != nil || create {
		name := ""
		if in.Name != nil {
			name = strings.TrimSpace(*in.Name)
		}
		if n := utf8.RuneCountInString(name); n < 1 || n > 40 {
			fields["name"] = "Name must be 1–40 characters"
		} else {
			h.Name = name
		}
	}
	if create {
		if in.Kind != nil && (*in.Kind == models.HabitCheck || *in.Kind == models.HabitCount) {
			h.Kind = *in.Kind
		} else {
			fields["kind"] = "Choose done or count"
		}
	}
	switch h.Kind {
	case models.HabitCheck:
		h.Target, h.Unit = 1, ""
	case models.HabitCount:
		if in.Target != nil || create {
			if in.Target == nil || *in.Target < 2 || *in.Target > 999 {
				fields["target"] = "Target must be 2–999"
			} else {
				h.Target = *in.Target
			}
		}
		if in.Unit != nil {
			unit := strings.TrimSpace(*in.Unit)
			if utf8.RuneCountInString(unit) > 12 {
				fields["unit"] = "Unit must be at most 12 characters"
			} else {
				h.Unit = unit
			}
		}
	}
	if in.Days != nil || create {
		var days []int
		if in.Days != nil {
			days = *in.Days
		}
		if w, ok := ParseDays(days); ok {
			h.Days = int(w)
		} else {
			fields["days"] = "Pick at least one day"
		}
	}
	if in.Hidden != nil {
		h.Hidden = *in.Hidden
	}
	// A new or newly unhidden habit takes an active slot.
	if len(fields) == 0 && !h.Hidden && (create || (in.Hidden != nil && !*in.Hidden)) {
		n, max, err := activeStats(tx, userID)
		if err != nil {
			return err
		}
		if n >= maxActiveHabits {
			fields["name"] = "You can have up to 30 habits"
		} else if create {
			h.SortOrder = max + 1
		}
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	return nil
}

func createHabit(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		var in habitInput
		if err := httpx.Bind(c, &in); err != nil {
			return err
		}
		in.Hidden = nil
		h := models.Habit{UserID: u.ID}
		err := d.DB.Transaction(func(tx *gorm.DB) error {
			if err := applyHabit(tx, u.ID, &h, in, true); err != nil {
				return err
			}
			return tx.Create(&h).Error
		})
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return errHabitTaken
		}
		if err != nil {
			return err
		}
		return c.Status(201).JSON(fiber.Map{"habit": habitDTO(h)})
	}
}

func updateHabit(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		var in habitInput
		if err := httpx.Bind(c, &in); err != nil {
			return err
		}
		var h models.Habit
		err = d.DB.Transaction(func(tx *gorm.DB) error {
			var err error
			if h, err = loadHabit(tx, u.ID, id); err != nil {
				return err
			}
			wasHidden := h.Hidden
			in.Kind = nil
			if in.Hidden != nil && *in.Hidden == wasHidden {
				in.Hidden = nil // no change, so no slot check
			}
			if err := applyHabit(tx, u.ID, &h, in, false); err != nil {
				return err
			}
			return tx.Save(&h).Error
		})
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return errHabitTaken
		}
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"habit": habitDTO(h)})
	}
}

func orderHabits(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		var in struct {
			IDs []string `json:"ids"`
		}
		if err := httpx.Bind(c, &in); err != nil {
			return err
		}
		var have []uuid.UUID
		if err := d.DB.Model(&models.Habit{}).Where("user_id = ? AND NOT hidden", u.ID).Pluck("id", &have).Error; err != nil {
			return err
		}
		valid := len(in.IDs) == len(have)
		want := map[uuid.UUID]bool{}
		for _, h := range have {
			want[h] = true
		}
		ids := make([]uuid.UUID, 0, len(in.IDs))
		for _, s := range in.IDs {
			id, err := uuid.Parse(s)
			if err != nil || !want[id] {
				valid = false
				break
			}
			delete(want, id) // a repeated id is no longer in the set
			ids = append(ids, id)
		}
		if !valid {
			return httpx.NewError(400, "INVALID_ORDER", "The list must contain each active habit exactly once")
		}
		err := d.DB.Transaction(func(tx *gorm.DB) error {
			for i, id := range ids {
				if err := tx.Model(&models.Habit{}).Where("id = ? AND user_id = ?", id, u.ID).
					Update("sort_order", i+1).Error; err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		return c.SendStatus(204)
	}
}

func deleteHabit(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		err = d.DB.Transaction(func(tx *gorm.DB) error {
			if _, err := loadHabit(tx, u.ID, id); err != nil {
				return err
			}
			if err := tx.Where("habit_id = ? AND user_id = ?", id, u.ID).Delete(&models.HabitLog{}).Error; err != nil {
				return err
			}
			return tx.Where("id = ? AND user_id = ?", id, u.ID).Delete(&models.Habit{}).Error
		})
		if err != nil {
			return err
		}
		return c.SendStatus(204)
	}
}
