package daily

import (
	"beprod/internal/app"
	"beprod/internal/httpx"
	"beprod/internal/session"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// orderActive handles PUT .../order for any user-owned model (newModel returns a fresh value per use, since gorm writes into it) with hidden and sort_order columns.
func orderActive(d app.Deps, newModel func() any, noun string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		var in struct {
			IDs []string `json:"ids"`
		}
		if err := httpx.Bind(c, &in); err != nil {
			return err
		}
		var have []uuid.UUID
		if err := d.DB.Model(newModel()).Where("user_id = ? AND NOT hidden", u.ID).Pluck("id", &have).Error; err != nil {
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
			return httpx.NewError(400, "INVALID_ORDER", "The list must contain each active "+noun+" exactly once")
		}
		err := d.DB.Transaction(func(tx *gorm.DB) error {
			for i, id := range ids {
				if err := tx.Model(newModel()).Where("id = ? AND user_id = ?", id, u.ID).
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
