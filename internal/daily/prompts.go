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

const maxActivePrompts = 20

// PromptDTO is the NotePrompt object of the API contract.
type PromptDTO struct {
	ID     uuid.UUID `json:"id"`
	Label  string    `json:"label"`
	Kind   string    `json:"kind"`
	Hidden bool      `json:"hidden"`
}

func promptDTO(p models.NotePrompt) PromptDTO {
	return PromptDTO{ID: p.ID, Label: p.Label, Kind: p.Kind, Hidden: p.Hidden}
}

func promptDTOs(ps []models.NotePrompt) []PromptDTO {
	out := make([]PromptDTO, 0, len(ps))
	for _, p := range ps {
		out = append(out, promptDTO(p))
	}
	return out
}

var errPromptTaken = httpx.Validation(map[string]string{"label": "You already have this prompt"})

func loadPrompt(db *gorm.DB, userID, id uuid.UUID) (models.NotePrompt, error) {
	var p models.NotePrompt
	err := db.Where("id = ? AND user_id = ?", id, userID).First(&p).Error
	return p, err
}

func listPrompts(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		var active, hidden []models.NotePrompt
		if err := d.DB.Where("user_id = ? AND NOT hidden", u.ID).Order("sort_order ASC, created_at ASC").Find(&active).Error; err != nil {
			return err
		}
		if err := d.DB.Where("user_id = ? AND hidden", u.ID).Order("lower(label) ASC").Find(&hidden).Error; err != nil {
			return err
		}
		return c.JSON(fiber.Map{"prompts": promptDTOs(active), "hidden": promptDTOs(hidden)})
	}
}

type promptInput struct {
	Label  *string `json:"label"`
	Kind   *string `json:"kind"` // create only
	Hidden *bool   `json:"hidden"`
}

// applyPrompt validates in against p's current values (zero for create) and writes valid values into p.
func applyPrompt(tx *gorm.DB, userID uuid.UUID, p *models.NotePrompt, in promptInput, create bool) error {
	fields := map[string]string{}
	if in.Label != nil || create {
		label := ""
		if in.Label != nil {
			label = strings.TrimSpace(*in.Label)
		}
		if n := utf8.RuneCountInString(label); n < 1 || n > 60 {
			fields["label"] = "Label must be 1–60 characters"
		} else {
			p.Label = label
		}
	}
	if create {
		if in.Kind != nil && (*in.Kind == models.PromptText || *in.Kind == models.PromptRating) {
			p.Kind = *in.Kind
		} else {
			fields["kind"] = "Choose text or rating"
		}
	}
	if in.Hidden != nil {
		p.Hidden = *in.Hidden
	}
	// A new or newly unhidden prompt takes an active slot and goes last.
	if len(fields) == 0 && !p.Hidden && (create || in.Hidden != nil) {
		var n int64
		var maxOrder int
		err := tx.Model(&models.NotePrompt{}).Where("user_id = ? AND NOT hidden", userID).
			Select("COUNT(*), COALESCE(MAX(sort_order), 0)").Row().Scan(&n, &maxOrder)
		if err != nil {
			return err
		}
		if n >= maxActivePrompts {
			fields["label"] = "You can have up to 20 prompts"
		} else {
			p.SortOrder = maxOrder + 1
		}
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	return nil
}

func createPrompt(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		var in promptInput
		if err := httpx.Bind(c, &in); err != nil {
			return err
		}
		in.Hidden = nil
		p := models.NotePrompt{UserID: u.ID}
		err := d.DB.Transaction(func(tx *gorm.DB) error {
			if err := applyPrompt(tx, u.ID, &p, in, true); err != nil {
				return err
			}
			return tx.Create(&p).Error
		})
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return errPromptTaken
		}
		if err != nil {
			return err
		}
		return c.Status(201).JSON(fiber.Map{"prompt": promptDTO(p)})
	}
}

func updatePrompt(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		var in promptInput
		if err := httpx.Bind(c, &in); err != nil {
			return err
		}
		var p models.NotePrompt
		err = d.DB.Transaction(func(tx *gorm.DB) error {
			var err error
			if p, err = loadPrompt(tx, u.ID, id); err != nil {
				return err
			}
			in.Kind = nil
			if in.Hidden != nil && *in.Hidden == p.Hidden {
				in.Hidden = nil // no change, so no slot check
			}
			if err := applyPrompt(tx, u.ID, &p, in, false); err != nil {
				return err
			}
			return tx.Save(&p).Error
		})
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return errPromptTaken
		}
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"prompt": promptDTO(p)})
	}
}

func deletePrompt(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		err = d.DB.Transaction(func(tx *gorm.DB) error {
			if _, err := loadPrompt(tx, u.ID, id); err != nil {
				return err
			}
			if err := tx.Where("prompt_id = ? AND user_id = ?", id, u.ID).Delete(&models.NoteEntry{}).Error; err != nil {
				return err
			}
			return tx.Where("id = ? AND user_id = ?", id, u.ID).Delete(&models.NotePrompt{}).Error
		})
		if err != nil {
			return err
		}
		return c.SendStatus(204)
	}
}
