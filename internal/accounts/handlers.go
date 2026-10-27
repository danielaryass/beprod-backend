package accounts

import (
	"errors"

	"beprod/internal/app"
	"beprod/internal/httpx"
	"beprod/internal/jakarta"
	"beprod/internal/models"
	"beprod/internal/session"
	"beprod/internal/transactions"

	"github.com/gofiber/fiber/v2"
	"github.com/lib/pq"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func Register(api fiber.Router, d app.Deps) {
	g := api.Group("/accounts", session.Require(d))
	g.Get("/", listHandler(d))
	g.Post("/", createHandler(d))
	g.Put("/order", orderHandler(d))
	g.Get("/:id", getHandler(d))
	g.Patch("/:id", updateHandler(d))
	g.Get("/:id/delete-preview", previewHandler(d))
	g.Delete("/:id", deleteHandler(d))
	g.Post("/:id/unarchive", unarchiveHandler(d))
	g.Post("/:id/adjust", adjustHandler(d))
}

func errNameTaken() *httpx.Error {
	return httpx.NewError(409, "ACCOUNT_NAME_TAKEN", "You already have an account with this name.")
}

func listHandler(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		active, archived, total, err := List(d.DB, u.ID)
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"accounts": active, "archived": archived, "totalBalance": total})
	}
}

func createHandler(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		var in input
		if err := httpx.Bind(c, &in); err != nil {
			return err
		}
		a := models.Account{UserID: u.ID, Status: models.StatusActive}
		if fields := applyInput(&a, in, true); len(fields) > 0 {
			return httpx.Validation(fields)
		}
		err := d.DB.Transaction(func(tx *gorm.DB) error {
			var count int64
			if err := tx.Model(&models.Account{}).Where("user_id = ? AND status = ?", u.ID, models.StatusActive).Count(&count).Error; err != nil {
				return err
			}
			if count >= maxActiveAccounts {
				return httpx.NewError(400, "ACCOUNT_LIMIT", "You can have at most 50 active accounts.")
			}
			var max int
			if err := tx.Model(&models.Account{}).Where("user_id = ? AND status = ?", u.ID, models.StatusActive).
				Select("COALESCE(MAX(sort_order),0)").Scan(&max).Error; err != nil {
				return err
			}
			a.SortOrder = max + 1
			return tx.Create(&a).Error
		})
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return errNameTaken()
		}
		if err != nil {
			return err
		}
		dto, err := ToDTOCard(d.DB, a, a.OpeningBalance)
		if err != nil {
			return err
		}
		return c.Status(201).JSON(fiber.Map{"account": dto})
	}
}

func getHandler(d app.Deps) fiber.Handler {
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

func updateHandler(d app.Deps) fiber.Handler {
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
		var in input
		if err := httpx.Bind(c, &in); err != nil {
			return err
		}
		stored := acc.OpeningBalance
		if fields := applyInput(&acc, in, false); len(fields) > 0 {
			return httpx.Validation(fields)
		}
		if acc.OpeningBalance != stored {
			has, err := hasTransactions(d.DB, acc.ID)
			if err != nil {
				return err
			}
			if has {
				return httpx.Validation(map[string]string{"openingBalance": "This account has transactions. Use Adjust balance instead."})
			}
		}
		if err := d.DB.Save(&acc).Error; err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return errNameTaken()
			}
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

// adjustHandler records the gap between the stored balance and the real one as a signed adjustment transaction.
func adjustHandler(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		var in struct {
			Balance *int64 `json:"balance"`
		}
		if err := httpx.Bind(c, &in); err != nil {
			return err
		}
		var acc models.Account
		var txn models.Transaction
		err = d.DB.Transaction(func(tx *gorm.DB) error {
			var rows []models.Account
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", id, u.ID).Limit(1).Find(&rows).Error; err != nil {
				return err
			}
			if len(rows) == 0 {
				return httpx.NotFound()
			}
			acc = rows[0]
			if acc.Status == models.StatusArchived {
				return httpx.NewError(400, "ACCOUNT_ARCHIVED", "Unarchive this account first")
			}
			if in.Balance == nil || *in.Balance < -maxBalance || *in.Balance > maxBalance {
				return httpx.Validation(map[string]string{"balance": "Balance is out of range"})
			}
			bal, err := Balances(tx, u.ID)
			if err != nil {
				return err
			}
			diff := *in.Balance - bal[acc.ID]
			if diff == 0 {
				return httpx.NewError(400, "NO_CHANGE", "Balance already matches")
			}
			txn = models.Transaction{UserID: u.ID, Type: models.TypeAdjustment, Amount: diff, OccurredAt: jakarta.Now(),
				AccountID: acc.ID, Note: "Balance adjustment", Tags: pq.StringArray{}, Source: models.SourceApp}
			return tx.Create(&txn).Error
		})
		if err != nil {
			return err
		}
		tdto, err := transactions.ToDTOs(d.DB, u.ID, []models.Transaction{txn})
		if err != nil {
			return err
		}
		bal, err := Balances(d.DB, u.ID)
		if err != nil {
			return err
		}
		adto, err := ToDTOCard(d.DB, acc, bal[acc.ID])
		if err != nil {
			return err
		}
		return c.Status(201).JSON(fiber.Map{"transaction": tdto[0], "account": adto})
	}
}
