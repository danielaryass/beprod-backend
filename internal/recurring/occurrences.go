package recurring

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"beprod/internal/app"
	"beprod/internal/httpx"
	"beprod/internal/jakarta"
	"beprod/internal/models"
	"beprod/internal/session"
	"beprod/internal/transactions"
)

var errNotPending = httpx.NewError(409, "OCCURRENCE_NOT_PENDING", "This item was already handled")

func loadOccurrence(db *gorm.DB, userID, id uuid.UUID) (models.RecurringOccurrence, error) {
	var o models.RecurringOccurrence
	err := db.Where("id = ? AND user_id = ?", id, userID).First(&o).Error
	return o, err
}

type confirmReq struct {
	Amount      *int64    `json:"amount"`
	Date        *string   `json:"date"`
	AccountID   *string   `json:"accountId"`
	ToAccountID *string   `json:"toAccountId"`
	CategoryID  *string   `json:"categoryId"`
	Note        *string   `json:"note"`
	Tags        *[]string `json:"tags"`
}

func confirmHandler(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		user := session.User(c)
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		occ, err := loadOccurrence(d.DB, user.ID, id)
		if err != nil {
			return err
		}
		var req confirmReq
		if len(c.Body()) > 0 {
			if err := httpx.Bind(c, &req); err != nil {
				return err
			}
		}
		if occ.Status != models.OccPending {
			return errNotPending
		}

		f := transactions.Fields{
			Type:      occ.Type,
			Amount:    occ.Amount,
			AccountID: occ.AccountID.String(),
			Note:      occ.Note,
			Tags:      []string(occ.Tags),
		}
		if occ.ToAccountID != nil {
			f.ToAccountID = occ.ToAccountID.String()
		}
		if occ.CategoryID != nil {
			f.CategoryID = occ.CategoryID.String()
		}
		if req.Amount != nil {
			f.Amount = *req.Amount
		}
		if req.AccountID != nil {
			f.AccountID = *req.AccountID
		}
		if req.ToAccountID != nil {
			f.ToAccountID = *req.ToAccountID
		}
		if req.CategoryID != nil {
			f.CategoryID = *req.CategoryID
		}
		if req.Note != nil {
			f.Note = *req.Note
		}
		if req.Tags != nil {
			f.Tags = *req.Tags
		}

		res, fields, err := transactions.ValidateFields(d.DB, user.ID, f, nil)
		if err != nil {
			return err
		}
		if fields == nil {
			fields = map[string]string{}
		}

		date := occ.DueDate
		if req.Date != nil {
			if p, perr := jakarta.ParseDate(*req.Date); perr != nil {
				fields["date"] = "Enter a valid date"
			} else if p.After(jakarta.Today()) {
				fields["date"] = "Date can't be in the future"
			} else {
				date = p
			}
		}
		if len(fields) > 0 {
			return httpx.Validation(fields)
		}

		var txn models.Transaction
		err = d.DB.Transaction(func(tx *gorm.DB) error {
			var ierr error
			txn, ierr = transactions.Insert(tx, user.ID, res, jakarta.StartOf(date), models.SourceRecurring, &occ.ID, nil)
			if ierr != nil {
				return ierr
			}
			r := tx.Model(&models.RecurringOccurrence{}).Where("id = ? AND status = ?", occ.ID, models.OccPending).
				Updates(map[string]any{"status": models.OccConfirmed, "transaction_id": txn.ID})
			if r.Error != nil {
				return r.Error
			}
			if r.RowsAffected == 0 {
				return errNotPending
			}
			return nil
		})
		if err != nil {
			return err
		}
		dtos, err := transactions.ToDTOs(d.DB, user.ID, []models.Transaction{txn})
		if err != nil {
			return err
		}
		return c.Status(201).JSON(fiber.Map{"transaction": dtos[0]})
	}
}

func skipHandler(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		user := session.User(c)
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		occ, err := loadOccurrence(d.DB, user.ID, id)
		if err != nil {
			return err
		}
		if occ.Status != models.OccPending {
			return errNotPending
		}
		r := d.DB.Model(&models.RecurringOccurrence{}).Where("id = ? AND status = ?", occ.ID, models.OccPending).
			Update("status", models.OccSkipped)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected == 0 {
			return errNotPending
		}
		return c.SendStatus(204)
	}
}
