package categories

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"beprod/internal/app"
	"beprod/internal/httpx"
	"beprod/internal/models"
	"beprod/internal/session"
)

func Register(api fiber.Router, d app.Deps) {
	g := api.Group("/categories", session.Require(d))
	g.Get("/", listHandler(d))
	g.Get("/meta", metaHandler(d))
	g.Post("/", createHandler(d))
	g.Put("/order", orderHandler(d))
	g.Patch("/:id", updateHandler(d))
	g.Get("/:id/delete-preview", previewHandler(d))
	g.Delete("/:id", deleteHandler(d))
	g.Post("/:id/unarchive", unarchiveHandler(d))
}

const msgChooseType = "Choose income or expense"

func errLimit() *httpx.Error {
	return httpx.NewError(400, "CATEGORY_LIMIT", "You can have at most 100 active categories of this type.")
}

func errNameTaken() *httpx.Error {
	return httpx.NewError(409, "CATEGORY_NAME_TAKEN", "This category already exists.")
}

// activeStats returns the number and the max sort_order of the user's active categories of a type.
func activeStats(db *gorm.DB, userID uuid.UUID, typ string) (count int64, maxOrder int, err error) {
	err = db.Model(&models.Category{}).
		Where("user_id = ? AND type = ? AND status = ?", userID, typ, models.StatusActive).
		Select("COUNT(*), COALESCE(MAX(sort_order), 0)").Row().Scan(&count, &maxOrder)
	return
}

func listHandler(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		typ := c.Query("type")
		if typ != "" && !validType(typ) {
			return httpx.Validation(map[string]string{"type": msgChooseType})
		}
		q := func(status, order string) ([]models.Category, error) {
			var cs []models.Category
			tx := d.DB.Where("user_id = ? AND status = ?", u.ID, status)
			if typ != "" {
				tx = tx.Where("type = ?", typ)
			}
			return cs, tx.Order(order).Find(&cs).Error
		}
		active, err := q(models.StatusActive, "type ASC, sort_order ASC, created_at ASC")
		if err != nil {
			return err
		}
		archived, err := q(models.StatusArchived, "type ASC, name ASC")
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"categories": toDTOs(active), "archived": toDTOs(archived)})
	}
}

func metaHandler(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"icons": Icons, "colors": Colors})
	}
}

func createHandler(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		var in struct {
			Name  string `json:"name"`
			Type  string `json:"type"`
			Icon  string `json:"icon"`
			Color string `json:"color"`
		}
		if err := httpx.Bind(c, &in); err != nil {
			return err
		}
		fields := map[string]string{}
		name, msg := validateName(in.Name)
		if msg != "" {
			fields["name"] = msg
		}
		if !validType(in.Type) {
			fields["type"] = msgChooseType
		}
		if m := validateIcon(in.Icon); m != "" {
			fields["icon"] = m
		}
		if m := validateColor(in.Color); m != "" {
			fields["color"] = m
		}
		if len(fields) > 0 {
			return httpx.Validation(fields)
		}
		cat := models.Category{UserID: u.ID, Name: name, Type: in.Type, Icon: in.Icon, Color: in.Color, Status: models.StatusActive}
		err := d.DB.Transaction(func(tx *gorm.DB) error {
			n, maxOrder, err := activeStats(tx, u.ID, in.Type)
			if err != nil {
				return err
			}
			if n >= maxActivePerType {
				return errLimit()
			}
			cat.SortOrder = maxOrder + 1
			return tx.Create(&cat).Error
		})
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return errNameTaken()
		}
		if err != nil {
			return err
		}
		return c.Status(201).JSON(fiber.Map{"category": ToDTO(cat)})
	}
}

func updateHandler(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		cat, err := loadOwned(d.DB, u.ID, id)
		if err != nil {
			return err
		}
		var in struct{ Name, Icon, Color *string }
		if err := httpx.Bind(c, &in); err != nil {
			return err
		}
		fields := map[string]string{}
		if in.Name != nil {
			name, msg := validateName(*in.Name)
			if msg != "" {
				fields["name"] = msg
			}
			cat.Name = name
		}
		if in.Icon != nil {
			if m := validateIcon(*in.Icon); m != "" {
				fields["icon"] = m
			}
			cat.Icon = *in.Icon
		}
		if in.Color != nil {
			if m := validateColor(*in.Color); m != "" {
				fields["color"] = m
			}
			cat.Color = *in.Color
		}
		if len(fields) > 0 {
			return httpx.Validation(fields)
		}
		if err := d.DB.Save(&cat).Error; err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return errNameTaken()
			}
			return err
		}
		return c.JSON(fiber.Map{"category": ToDTO(cat)})
	}
}

type catUsage struct {
	HasTransactions bool
	HasBudget       bool  // budget of ANY status for this category
	AnyRules        int64 // recurring rules of any status
	ActiveRules     int64 // status = 'active'
}

func (u catUsage) used() bool { return u.HasTransactions || u.HasBudget || u.AnyRules > 0 }

func categoryUsage(db *gorm.DB, userID, categoryID uuid.UUID) (catUsage, error) {
	var out catUsage
	count := func(model any, extra string) (n int64, err error) {
		tx := db.Model(model).Where("user_id = ? AND category_id = ?", userID, categoryID)
		if extra != "" {
			tx = tx.Where(extra)
		}
		return n, tx.Count(&n).Error
	}
	n, err := count(&models.Transaction{}, "")
	if err != nil {
		return out, err
	}
	out.HasTransactions = n > 0
	if n, err = count(&models.Budget{}, ""); err != nil {
		return out, err
	}
	out.HasBudget = n > 0
	if out.AnyRules, err = count(&models.RecurringRule{}, ""); err != nil {
		return out, err
	}
	out.ActiveRules, err = count(&models.RecurringRule{}, "status = 'active'")
	return out, err
}

func previewHandler(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		if _, err := loadOwned(d.DB, u.ID, id); err != nil {
			return err
		}
		use, err := categoryUsage(d.DB, u.ID, id)
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{
			"willArchive":          use.used(),
			"hasTransactions":      use.HasTransactions,
			"hasBudget":            use.HasBudget,
			"activeRecurringRules": use.ActiveRules,
		})
	}
}

func deleteHandler(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		var resp fiber.Map
		err = d.DB.Transaction(func(tx *gorm.DB) error {
			if _, err := loadOwned(tx, u.ID, id); err != nil {
				return err
			}
			use, err := categoryUsage(tx, u.ID, id)
			if err != nil {
				return err
			}
			if !use.used() {
				resp = fiber.Map{"result": "deleted"}
				return tx.Where("id = ? AND user_id = ?", id, u.ID).Delete(&models.Category{}).Error
			}
			if err := tx.Model(&models.Category{}).Where("id = ? AND user_id = ?", id, u.ID).
				Update("status", models.StatusArchived).Error; err != nil {
				return err
			}
			b := tx.Model(&models.Budget{}).
				Where("user_id = ? AND category_id = ? AND status = ?", u.ID, id, models.StatusActive).
				Update("status", models.StatusInactive)
			if b.Error != nil {
				return b.Error
			}
			r := tx.Model(&models.RecurringRule{}).
				Where("user_id = ? AND category_id = ? AND status = ?", u.ID, id, models.StatusActive).
				Updates(map[string]any{"status": models.RulePaused, "paused_reason": models.PausedCategoryArchived})
			if r.Error != nil {
				return r.Error
			}
			resp = fiber.Map{"result": "archived", "pausedRules": r.RowsAffected, "budgetDeactivated": b.RowsAffected > 0}
			return nil
		})
		if err != nil {
			return err
		}
		return c.JSON(resp)
	}
}

func unarchiveHandler(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		cat, err := loadOwned(d.DB, u.ID, id)
		if err != nil {
			return err
		}
		if cat.Status != models.StatusArchived {
			return httpx.NewError(400, "NOT_ARCHIVED", "This category is not archived")
		}
		n, maxOrder, err := activeStats(d.DB, u.ID, cat.Type)
		if err != nil {
			return err
		}
		if n >= maxActivePerType {
			return errLimit()
		}
		// single statement: a unique violation aborts a surrounding Postgres transaction
		err = d.DB.Model(&models.Category{}).Where("id = ? AND user_id = ?", id, u.ID).
			Updates(map[string]any{"status": models.StatusActive, "sort_order": maxOrder + 1}).Error
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return httpx.NewError(409, "CATEGORY_NAME_TAKEN", "Rename this category first: an active category already uses this name.")
		}
		if err != nil {
			return err
		}
		if cat, err = loadOwned(d.DB, u.ID, id); err != nil {
			return err
		}
		return c.JSON(fiber.Map{"category": ToDTO(cat)})
	}
}

func orderHandler(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		var in struct {
			Type string   `json:"type"`
			IDs  []string `json:"ids"`
		}
		if err := httpx.Bind(c, &in); err != nil {
			return err
		}
		if !validType(in.Type) {
			return httpx.Validation(map[string]string{"type": msgChooseType})
		}
		var have []uuid.UUID
		if err := d.DB.Model(&models.Category{}).
			Where("user_id = ? AND type = ? AND status = ?", u.ID, in.Type, models.StatusActive).
			Pluck("id", &have).Error; err != nil {
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
			return httpx.NewError(400, "INVALID_ORDER", "The list must contain each active category of this type exactly once")
		}
		err := d.DB.Transaction(func(tx *gorm.DB) error {
			for i, id := range ids {
				if err := tx.Model(&models.Category{}).Where("id = ? AND user_id = ?", id, u.ID).
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
