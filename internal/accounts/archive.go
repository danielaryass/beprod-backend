package accounts

import (
	"errors"

	"beprod/internal/app"
	"beprod/internal/httpx"
	"beprod/internal/models"
	"beprod/internal/session"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// usage reports how the account is referenced by the user's data.
type usage struct {
	HasTransactions bool  // any transaction with account_id = id OR to_account_id = id
	AnyRules        int64 // recurring rules of ANY status with account_id = id OR to_account_id = id
	ActiveRules     int64 // same, but status = 'active'
}

func accountUsage(db *gorm.DB, userID, accountID uuid.UUID) (usage, error) {
	const cond = "user_id = ? AND (account_id = ? OR to_account_id = ?)"
	var u usage
	var n int64
	if err := db.Model(&models.Transaction{}).Where(cond, userID, accountID, accountID).Count(&n).Error; err != nil {
		return u, err
	}
	u.HasTransactions = n > 0
	if err := db.Model(&models.RecurringRule{}).Where(cond, userID, accountID, accountID).Count(&u.AnyRules).Error; err != nil {
		return u, err
	}
	err := db.Model(&models.RecurringRule{}).Where(cond, userID, accountID, accountID).
		Where("status = ?", models.StatusActive).Count(&u.ActiveRules).Error
	return u, err
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
		us, err := accountUsage(d.DB, u.ID, id)
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{
			"willArchive":          us.HasTransactions || us.AnyRules > 0,
			"hasTransactions":      us.HasTransactions,
			"activeRecurringRules": us.ActiveRules,
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
		var out fiber.Map
		err = d.DB.Transaction(func(tx *gorm.DB) error {
			if _, err := loadOwned(tx, u.ID, id); err != nil {
				return err
			}
			var blocking []models.Goal
			if err := tx.Where("user_id = ? AND account_id = ? AND status = ?", u.ID, id, models.GoalActive).
				Limit(1).Find(&blocking).Error; err != nil {
				return err
			}
			if len(blocking) > 0 {
				return httpx.NewError(409, "ACCOUNT_HAS_GOAL", `This account is used by goal "`+blocking[0].Name+`"`)
			}
			us, err := accountUsage(tx, u.ID, id)
			if err != nil {
				return err
			}
			if !us.HasTransactions && us.AnyRules == 0 {
				if err := tx.Where("account_id = ? AND user_id = ?", id, u.ID).Delete(&models.Goal{}).Error; err != nil {
					return err
				}
				if err := tx.Where("id = ? AND user_id = ?", id, u.ID).Delete(&models.Account{}).Error; err != nil {
					return err
				}
				out = fiber.Map{"result": "deleted"}
				return nil
			}
			if err := tx.Model(&models.Account{}).Where("id = ? AND user_id = ?", id, u.ID).
				Update("status", models.StatusArchived).Error; err != nil {
				return err
			}
			res := tx.Model(&models.RecurringRule{}).
				Where("user_id = ? AND status = ? AND (account_id = ? OR to_account_id = ?)", u.ID, models.StatusActive, id, id).
				Updates(map[string]any{"status": models.RulePaused, "paused_reason": models.PausedAccountArchived})
			if res.Error != nil {
				return res.Error
			}
			out = fiber.Map{"result": "archived", "pausedRules": res.RowsAffected}
			return nil
		})
		if err != nil {
			return err
		}
		return c.JSON(out)
	}
}

func unarchiveHandler(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		acc, err := loadOwned(d.DB, u.ID, id)
		if err != nil {
			return err
		}
		if acc.Status != models.StatusArchived {
			return httpx.NewError(400, "NOT_ARCHIVED", "This account is not archived")
		}
		active := d.DB.Model(&models.Account{}).Where("user_id = ? AND status = ?", u.ID, models.StatusActive)
		var count int64
		if err := active.Count(&count).Error; err != nil {
			return err
		}
		if count >= maxActiveAccounts {
			return httpx.NewError(400, "ACCOUNT_LIMIT", "You can have at most 50 active accounts.")
		}
		var max int
		if err := d.DB.Model(&models.Account{}).Where("user_id = ? AND status = ?", u.ID, models.StatusActive).
			Select("COALESCE(MAX(sort_order),0)").Scan(&max).Error; err != nil {
			return err
		}
		err = d.DB.Model(&models.Account{}).Where("id = ? AND user_id = ?", id, u.ID).
			Updates(map[string]any{"status": models.StatusActive, "sort_order": max + 1}).Error
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return httpx.NewError(409, "ACCOUNT_NAME_TAKEN", "Rename this account first: an active account already uses this name.")
		}
		if err != nil {
			return err
		}
		acc, err = loadOwned(d.DB, u.ID, id)
		if err != nil {
			return err
		}
		bal, err := Balances(d.DB, u.ID)
		if err != nil {
			return err
		}
		dto, err := ToDTOCard(d.DB, acc, bal[acc.ID])
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"account": dto})
	}
}

func orderHandler(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		var in struct {
			IDs []string `json:"ids"`
		}
		if err := httpx.Bind(c, &in); err != nil {
			return err
		}
		var activeIDs []uuid.UUID
		if err := d.DB.Model(&models.Account{}).Where("user_id = ? AND status = ?", u.ID, models.StatusActive).
			Pluck("id", &activeIDs).Error; err != nil {
			return err
		}
		invalid := httpx.NewError(400, "INVALID_ORDER", "The list must contain each active account exactly once")
		if len(in.IDs) != len(activeIDs) {
			return invalid
		}
		owned := make(map[uuid.UUID]bool, len(activeIDs))
		for _, id := range activeIDs {
			owned[id] = true
		}
		ids := make([]uuid.UUID, 0, len(in.IDs))
		seen := make(map[uuid.UUID]bool, len(in.IDs))
		for _, s := range in.IDs {
			id, err := uuid.Parse(s)
			if err != nil || seen[id] || !owned[id] {
				return invalid
			}
			seen[id] = true
			ids = append(ids, id)
		}
		err := d.DB.Transaction(func(tx *gorm.DB) error {
			for i, id := range ids {
				if err := tx.Model(&models.Account{}).Where("id = ? AND user_id = ?", id, u.ID).
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
