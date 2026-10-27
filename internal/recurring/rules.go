package recurring

import (
	"errors"
	"time"

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

var validFreq = map[string]bool{"daily": true, "weekly": true, "monthly": true, "yearly": true}

func loadRule(db *gorm.DB, userID, id uuid.UUID) (models.RecurringRule, error) {
	var r models.RecurringRule
	err := db.Where("id = ? AND user_id = ?", id, userID).First(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return r, httpx.NotFound()
	}
	return r, err
}

func ruleResponse(db *gorm.DB, userID uuid.UUID, r models.RecurringRule) (fiber.Map, error) {
	dtos, err := ToRuleDTOs(db, userID, []models.RecurringRule{r})
	if err != nil {
		return nil, err
	}
	return fiber.Map{"rule": dtos[0]}, nil
}

func listHandler(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		user := session.User(c)
		due, _, err := Pending(d.DB, user.ID, 0)
		if err != nil {
			return err
		}
		var rules []models.RecurringRule
		if err := d.DB.Where("user_id = ?", user.ID).Order("created_at ASC").Find(&rules).Error; err != nil {
			return err
		}
		dtos, err := ToRuleDTOs(d.DB, user.ID, rules)
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"due": due, "rules": dtos})
	}
}

type createReq struct {
	transactions.Fields
	Frequency string  `json:"frequency"`
	StartDate string  `json:"startDate"`
	EndDate   *string `json:"endDate"`
}

func createRule(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		user := session.User(c)
		var req createReq
		if err := httpx.Bind(c, &req); err != nil {
			return err
		}
		res, fields, err := transactions.ValidateFields(d.DB, user.ID, req.Fields, nil)
		if err != nil {
			return err
		}
		if fields == nil {
			fields = map[string]string{}
		}
		if !validFreq[req.Frequency] {
			fields["frequency"] = "Choose daily, weekly, monthly or yearly"
		}
		start, serr := jakarta.ParseDate(req.StartDate)
		if req.StartDate == "" || serr != nil {
			fields["startDate"] = "Enter a valid date"
		} else if start.Before(jakarta.Today()) {
			fields["startDate"] = "Start date can't be in the past"
		}
		var end *time.Time
		if req.EndDate != nil && *req.EndDate != "" {
			e, eerr := jakarta.ParseDate(*req.EndDate)
			if eerr != nil {
				fields["endDate"] = "Enter a valid date"
			} else {
				end = &e
				if serr == nil && req.StartDate != "" && e.Before(start) {
					fields["endDate"] = "End date must be on or after the start date"
				}
			}
		}
		if len(fields) > 0 {
			return httpx.Validation(fields)
		}
		rule := models.RecurringRule{
			UserID: user.ID, Type: res.Type, Amount: res.Amount, AccountID: res.AccountID,
			ToAccountID: res.ToAccountID, CategoryID: res.CategoryID, Note: res.Note, Tags: res.Tags,
			Frequency: req.Frequency, StartDate: start, EndDate: end, NextDueDate: start, Status: models.StatusActive,
		}
		err = d.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&rule).Error; err != nil {
				return err
			}
			return GenerateRule(tx, &rule, jakarta.Today())
		})
		if err != nil {
			return err
		}
		out, err := ruleResponse(d.DB, user.ID, rule)
		if err != nil {
			return err
		}
		return c.Status(201).JSON(out)
	}
}

type updateReq struct {
	Amount      int64    `json:"amount"`
	AccountID   string   `json:"accountId"`
	ToAccountID string   `json:"toAccountId"`
	CategoryID  string   `json:"categoryId"`
	Note        string   `json:"note"`
	Tags        []string `json:"tags"`
	EndDate     *string  `json:"endDate"`
}

func updateRule(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		user := session.User(c)
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		rule, err := loadRule(d.DB, user.ID, id)
		if err != nil {
			return err
		}
		var req updateReq
		if err := httpx.Bind(c, &req); err != nil {
			return err
		}
		f := transactions.Fields{Type: rule.Type, Amount: req.Amount, AccountID: req.AccountID,
			ToAccountID: req.ToAccountID, CategoryID: req.CategoryID, Note: req.Note, Tags: req.Tags}
		keep := &transactions.Resolved{Type: rule.Type, Amount: rule.Amount, AccountID: rule.AccountID,
			ToAccountID: rule.ToAccountID, CategoryID: rule.CategoryID, Note: rule.Note, Tags: rule.Tags}
		res, fields, err := transactions.ValidateFields(d.DB, user.ID, f, keep)
		if err != nil {
			return err
		}
		if fields == nil {
			fields = map[string]string{}
		}
		var end *time.Time
		if req.EndDate != nil && *req.EndDate != "" {
			e, eerr := jakarta.ParseDate(*req.EndDate)
			if eerr != nil {
				fields["endDate"] = "Enter a valid date"
			} else if e.Before(rule.StartDate) {
				fields["endDate"] = "End date must be on or after the start date"
			} else {
				end = &e
			}
		}
		if len(fields) > 0 {
			return httpx.Validation(fields)
		}
		rule.Amount, rule.AccountID, rule.ToAccountID, rule.CategoryID = res.Amount, res.AccountID, res.ToAccountID, res.CategoryID
		rule.Note, rule.Tags, rule.EndDate = res.Note, res.Tags, end
		if end != nil && end.Before(rule.NextDueDate) {
			rule.Status = models.RuleEnded
		}
		if err := d.DB.Save(&rule).Error; err != nil {
			return err
		}
		out, err := ruleResponse(d.DB, user.ID, rule)
		if err != nil {
			return err
		}
		return c.JSON(out)
	}
}

func pauseRule(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		user := session.User(c)
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		rule, err := loadRule(d.DB, user.ID, id)
		if err != nil {
			return err
		}
		if rule.Status != models.StatusActive {
			return httpx.NewError(400, "RULE_NOT_ACTIVE", "Only active rules can be paused")
		}
		if err := d.DB.Model(&models.RecurringRule{}).Where("id = ?", rule.ID).
			Updates(map[string]any{"status": models.RulePaused, "paused_reason": models.PausedByUser}).Error; err != nil {
			return err
		}
		rule, err = loadRule(d.DB, user.ID, id)
		if err != nil {
			return err
		}
		out, err := ruleResponse(d.DB, user.ID, rule)
		if err != nil {
			return err
		}
		return c.JSON(out)
	}
}

func resumeRule(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		user := session.User(c)
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		rule, err := loadRule(d.DB, user.ID, id)
		if err != nil {
			return err
		}
		if rule.Status != models.RulePaused {
			return httpx.NewError(400, "RULE_NOT_PAUSED", "Only paused rules can be resumed")
		}
		accIDs := []uuid.UUID{rule.AccountID}
		if rule.ToAccountID != nil {
			accIDs = append(accIDs, *rule.ToAccountID)
		}
		var archived int64
		if err := d.DB.Model(&models.Account{}).
			Where("user_id = ? AND id IN ? AND status = ?", user.ID, accIDs, models.StatusArchived).Count(&archived).Error; err != nil {
			return err
		}
		if rule.CategoryID != nil {
			var n int64
			if err := d.DB.Model(&models.Category{}).
				Where("user_id = ? AND id = ? AND status = ?", user.ID, *rule.CategoryID, models.StatusArchived).Count(&n).Error; err != nil {
				return err
			}
			archived += n
		}
		if archived > 0 {
			return httpx.NewError(400, "RULE_REFS_ARCHIVED", "Edit this rule to use an active account and category first")
		}
		err = d.DB.Transaction(func(tx *gorm.DB) error {
			today := jakarta.Today()
			next := FirstOnOrAfter(rule.StartDate, rule.Frequency, today) // no back-fill
			rule.NextDueDate = next
			rule.PausedReason = ""
			if rule.EndDate != nil && next.After(*rule.EndDate) {
				rule.Status = models.RuleEnded
			} else {
				rule.Status = models.StatusActive
			}
			if err := tx.Save(&rule).Error; err != nil {
				return err
			}
			if rule.Status == models.StatusActive {
				return GenerateRule(tx, &rule, today)
			}
			return nil
		})
		if err != nil {
			return err
		}
		out, err := ruleResponse(d.DB, user.ID, rule)
		if err != nil {
			return err
		}
		return c.JSON(out)
	}
}

func deleteRule(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		user := session.User(c)
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		rule, err := loadRule(d.DB, user.ID, id)
		if err != nil {
			return err
		}
		err = d.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Where("rule_id = ? AND status = ?", rule.ID, models.OccPending).Delete(&models.RecurringOccurrence{}).Error; err != nil {
				return err
			}
			return tx.Delete(&models.RecurringRule{}, "id = ?", rule.ID).Error
		})
		if err != nil {
			return err
		}
		return c.SendStatus(204)
	}
}
