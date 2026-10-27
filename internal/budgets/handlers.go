package budgets

import (
	"errors"

	"beprod/internal/app"
	"beprod/internal/httpx"
	"beprod/internal/jakarta"
	"beprod/internal/models"
	"beprod/internal/session"
	"beprod/internal/transactions"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func Register(api fiber.Router, d app.Deps) {
	g := api.Group("/budgets", session.Require(d))
	g.Get("/", list(d))
	g.Post("/", create(d))
	g.Patch("/:id", update(d))
	g.Post("/:id/activate", activate(d))
	g.Delete("/:id", remove(d))
}

func viewOf(db *gorm.DB, userID, budgetID uuid.UUID) (View, error) {
	views, err := ForMonth(db, userID, CurrentMonth())
	if err != nil {
		return View{}, err
	}
	for _, v := range views {
		if v.ID == budgetID {
			return v, nil
		}
	}
	return View{}, gorm.ErrRecordNotFound
}

func loadBudget(db *gorm.DB, userID, id uuid.UUID) (models.Budget, error) {
	var b models.Budget
	err := db.Where("id = ? AND user_id = ?", id, userID).First(&b).Error
	return b, err
}

func validateLimit(v int64) string {
	switch {
	case v < 1:
		return "Budget must be greater than 0."
	case v > transactions.MaxAmount:
		return "Budget is too large"
	}
	return ""
}

func budgetExists() *httpx.Error {
	return httpx.NewError(409, "BUDGET_EXISTS", "This category already has a budget.")
}

func list(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		user := session.User(c)
		month := CurrentMonth()
		if q := c.Query("month"); q != "" {
			m, err := jakarta.ParseMonth(q)
			if err != nil {
				return httpx.Validation(map[string]string{"month": "Enter a valid month (YYYY-MM)"})
			}
			month = m
		}
		views, err := ForMonth(d.DB, user.ID, month)
		if err != nil {
			return err
		}
		inactive, err := Inactive(d.DB, user.ID)
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"month": jakarta.FormatMonth(month), "budgets": views, "inactive": inactive})
	}
}

type createReq struct {
	CategoryID      string `json:"categoryId"`
	MonthlyLimit    int64  `json:"monthlyLimit"`
	RolloverEnabled bool   `json:"rolloverEnabled"`
}

func create(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		user := session.User(c)
		var req createReq
		if err := httpx.Bind(c, &req); err != nil {
			return err
		}
		fields := map[string]string{}
		catID, err := uuid.Parse(req.CategoryID)
		if err == nil {
			var n int64
			if err := d.DB.Model(&models.Category{}).
				Where("id = ? AND user_id = ? AND type = ? AND status = ?", catID, user.ID, models.TypeExpense, models.StatusActive).
				Count(&n).Error; err != nil {
				return err
			}
			if n == 0 {
				err = gorm.ErrRecordNotFound
			}
		}
		if err != nil {
			fields["categoryId"] = "Choose an active expense category"
		}
		if msg := validateLimit(req.MonthlyLimit); msg != "" {
			fields["monthlyLimit"] = msg
		}
		if len(fields) > 0 {
			return httpx.Validation(fields)
		}

		var dup int64
		if err := d.DB.Model(&models.Budget{}).Where("user_id = ? AND category_id = ?", user.ID, catID).Count(&dup).Error; err != nil {
			return err
		}
		if dup > 0 {
			return budgetExists()
		}

		b := models.Budget{UserID: user.ID, CategoryID: catID, RolloverEnabled: req.RolloverEnabled, Status: models.StatusActive}
		err = d.DB.Transaction(func(tx *gorm.DB) error {
			cur := CurrentMonth()
			if req.RolloverEnabled {
				b.RolloverSince = &cur
			}
			if err := tx.Create(&b).Error; err != nil {
				return err
			}
			return tx.Create(&models.BudgetLimit{BudgetID: b.ID, EffectiveMonth: cur, MonthlyLimit: req.MonthlyLimit}).Error
		})
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return budgetExists()
		}
		if err != nil {
			return err
		}
		v, err := viewOf(d.DB, user.ID, b.ID)
		if err != nil {
			return err
		}
		return c.Status(201).JSON(fiber.Map{"budget": v})
	}
}

type updateReq struct {
	MonthlyLimit    *int64 `json:"monthlyLimit"`
	RolloverEnabled *bool  `json:"rolloverEnabled"`
}

func update(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		user := session.User(c)
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		b, err := loadBudget(d.DB, user.ID, id)
		if err != nil {
			return err
		}
		var req updateReq
		if err := httpx.Bind(c, &req); err != nil {
			return err
		}
		if req.MonthlyLimit != nil {
			if msg := validateLimit(*req.MonthlyLimit); msg != "" {
				return httpx.Validation(map[string]string{"monthlyLimit": msg})
			}
		}
		if b.Status != models.StatusActive {
			return httpx.NewError(400, "BUDGET_INACTIVE", "Activate this budget first")
		}
		cur := CurrentMonth()
		err = d.DB.Transaction(func(tx *gorm.DB) error {
			if req.MonthlyLimit != nil {
				if err := tx.Clauses(clause.OnConflict{
					Columns:   []clause.Column{{Name: "budget_id"}, {Name: "effective_month"}},
					DoUpdates: clause.AssignmentColumns([]string{"monthly_limit"}),
				}).Create(&models.BudgetLimit{BudgetID: b.ID, EffectiveMonth: cur, MonthlyLimit: *req.MonthlyLimit}).Error; err != nil {
					return err
				}
			}
			if req.RolloverEnabled != nil && *req.RolloverEnabled != b.RolloverEnabled {
				changes := map[string]any{"rollover_enabled": false, "rollover_since": nil}
				if *req.RolloverEnabled {
					changes = map[string]any{"rollover_enabled": true, "rollover_since": cur}
				}
				return tx.Model(&models.Budget{}).Where("id = ?", b.ID).Updates(changes).Error
			}
			return nil
		})
		if err != nil {
			return err
		}
		v, err := viewOf(d.DB, user.ID, b.ID)
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"budget": v})
	}
}

func activate(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		user := session.User(c)
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		b, err := loadBudget(d.DB, user.ID, id)
		if err != nil {
			return err
		}
		if b.Status != models.StatusActive {
			var cat models.Category
			if err := d.DB.Where("id = ? AND user_id = ?", b.CategoryID, user.ID).First(&cat).Error; err != nil {
				return err
			}
			if cat.Status != models.StatusActive {
				return httpx.NewError(400, "CATEGORY_ARCHIVED", "Unarchive the category first")
			}
			changes := map[string]any{"status": models.StatusActive}
			if b.RolloverEnabled {
				changes["rollover_since"] = CurrentMonth()
			}
			if err := d.DB.Model(&models.Budget{}).Where("id = ?", b.ID).Updates(changes).Error; err != nil {
				return err
			}
		}
		v, err := viewOf(d.DB, user.ID, b.ID)
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"budget": v})
	}
}

func remove(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		user := session.User(c)
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		b, err := loadBudget(d.DB, user.ID, id)
		if err != nil {
			return err
		}
		err = d.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Where("budget_id = ?", b.ID).Delete(&models.BudgetLimit{}).Error; err != nil {
				return err
			}
			return tx.Delete(&models.Budget{}, "id = ?", b.ID).Error
		})
		if err != nil {
			return err
		}
		return c.SendStatus(204)
	}
}
