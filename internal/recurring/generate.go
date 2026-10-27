package recurring

import (
	"log"
	"time"

	"github.com/lib/pq"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"beprod/internal/jakarta"
	"beprod/internal/models"
)

// GenerateRule creates pending occurrences for every due date <= today (snapshot of rule values), idempotent via
// ON CONFLICT (rule_id, due_date) DO NOTHING; advances next_due_date; sets status ended once the end date's occurrence
// exists (EndDate != nil && next_due_date > *EndDate). Only for status active.
func GenerateRule(tx *gorm.DB, rule *models.RecurringRule, today time.Time) error {
	if rule.Status != models.StatusActive {
		return nil
	}
	next := rule.NextDueDate
	for !next.After(today) && (rule.EndDate == nil || !next.After(*rule.EndDate)) {
		tags := rule.Tags
		if tags == nil {
			tags = pq.StringArray{}
		}
		occ := models.RecurringOccurrence{
			RuleID: rule.ID, UserID: rule.UserID, DueDate: next, Status: models.OccPending,
			Type: rule.Type, Amount: rule.Amount, AccountID: rule.AccountID,
			ToAccountID: rule.ToAccountID, CategoryID: rule.CategoryID, Note: rule.Note, Tags: tags,
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "rule_id"}, {Name: "due_date"}},
			DoNothing: true,
		}).Create(&occ).Error; err != nil {
			return err
		}
		next = NextAfter(rule.StartDate, rule.Frequency, next)
	}
	rule.NextDueDate = next
	if rule.EndDate != nil && next.After(*rule.EndDate) {
		rule.Status = models.RuleEnded
	}
	return tx.Model(&models.RecurringRule{}).Where("id = ?", rule.ID).
		Updates(map[string]any{"next_due_date": next, "status": rule.Status}).Error
}

// GenerateAll: all active rules with next_due_date <= jakarta.Today(); each rule in its own tx; log + continue on per-rule error.
func GenerateAll(db *gorm.DB) error {
	today := jakarta.Today()
	var rules []models.RecurringRule
	if err := db.Where("status = ? AND next_due_date <= ?", models.StatusActive, today).Find(&rules).Error; err != nil {
		return err
	}
	for i := range rules {
		err := db.Transaction(func(tx *gorm.DB) error { return GenerateRule(tx, &rules[i], today) })
		if err != nil {
			log.Printf("recurring: generate rule %s: %v", rules[i].ID, err)
		}
	}
	return nil
}
