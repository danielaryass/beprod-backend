package recurring

import (
	"github.com/google/uuid"
	"gorm.io/gorm"

	"beprod/internal/jakarta"
	"beprod/internal/models"
	"beprod/internal/transactions"
)

type RuleDTO struct {
	ID           uuid.UUID                 `json:"id"`
	Type         string                    `json:"type"`
	Amount       int64                     `json:"amount"`
	Account      transactions.AccountRef   `json:"account"`
	ToAccount    *transactions.AccountRef  `json:"toAccount"`
	Category     *transactions.CategoryRef `json:"category"`
	Note         string                    `json:"note"`
	Tags         []string                  `json:"tags"`
	Frequency    string                    `json:"frequency"`
	StartDate    string                    `json:"startDate"`
	EndDate      *string                   `json:"endDate"`
	NextDueDate  string                    `json:"nextDueDate"`
	Status       string                    `json:"status"`
	PausedReason string                    `json:"pausedReason"`
}

type OccurrenceDTO struct {
	ID            uuid.UUID                 `json:"id"`
	RuleID        uuid.UUID                 `json:"ruleId"`
	DueDate       string                    `json:"dueDate"`
	Status        string                    `json:"status"`
	Type          string                    `json:"type"`
	Amount        int64                     `json:"amount"`
	Account       transactions.AccountRef   `json:"account"`
	ToAccount     *transactions.AccountRef  `json:"toAccount"`
	Category      *transactions.CategoryRef `json:"category"`
	Note          string                    `json:"note"`
	Tags          []string                  `json:"tags"`
	TransactionID *uuid.UUID                `json:"transactionId"`
}

type refMaps struct {
	accounts   map[uuid.UUID]transactions.AccountRef
	categories map[uuid.UUID]transactions.CategoryRef
}

func refs(db *gorm.DB, userID uuid.UUID) (refMaps, error) {
	m := refMaps{map[uuid.UUID]transactions.AccountRef{}, map[uuid.UUID]transactions.CategoryRef{}}
	var accs []models.Account
	if err := db.Where("user_id = ?", userID).Find(&accs).Error; err != nil {
		return m, err
	}
	for _, a := range accs {
		m.accounts[a.ID] = transactions.AccountRef{ID: a.ID, Name: a.Name, Type: a.Type, Archived: a.Status == models.StatusArchived}
	}
	var cats []models.Category
	if err := db.Where("user_id = ?", userID).Find(&cats).Error; err != nil {
		return m, err
	}
	for _, c := range cats {
		m.categories[c.ID] = transactions.CategoryRef{ID: c.ID, Name: c.Name, Icon: c.Icon, Color: c.Color, Archived: c.Status == models.StatusArchived}
	}
	return m, nil
}

func (m refMaps) account(id uuid.UUID) transactions.AccountRef {
	if r, ok := m.accounts[id]; ok {
		return r
	}
	return transactions.AccountRef{ID: id}
}

func (m refMaps) toAccount(id *uuid.UUID) *transactions.AccountRef {
	if id == nil {
		return nil
	}
	r := m.account(*id)
	return &r
}

func (m refMaps) category(id *uuid.UUID) *transactions.CategoryRef {
	if id == nil {
		return nil
	}
	r, ok := m.categories[*id]
	if !ok {
		r = transactions.CategoryRef{ID: *id}
	}
	return &r
}

func tagsOf(t []string) []string {
	if t == nil {
		return []string{}
	}
	return append([]string{}, t...)
}

func ToRuleDTOs(db *gorm.DB, userID uuid.UUID, rules []models.RecurringRule) ([]RuleDTO, error) {
	out := []RuleDTO{}
	if len(rules) == 0 {
		return out, nil
	}
	m, err := refs(db, userID)
	if err != nil {
		return nil, err
	}
	for _, r := range rules {
		var end *string
		if r.EndDate != nil {
			s := jakarta.FormatDate(*r.EndDate)
			end = &s
		}
		out = append(out, RuleDTO{
			ID: r.ID, Type: r.Type, Amount: r.Amount,
			Account: m.account(r.AccountID), ToAccount: m.toAccount(r.ToAccountID), Category: m.category(r.CategoryID),
			Note: r.Note, Tags: tagsOf(r.Tags), Frequency: r.Frequency,
			StartDate: jakarta.FormatDate(r.StartDate), EndDate: end, NextDueDate: jakarta.FormatDate(r.NextDueDate),
			Status: r.Status, PausedReason: r.PausedReason,
		})
	}
	return out, nil
}

func ToOccurrenceDTOs(db *gorm.DB, userID uuid.UUID, occs []models.RecurringOccurrence) ([]OccurrenceDTO, error) {
	out := []OccurrenceDTO{}
	if len(occs) == 0 {
		return out, nil
	}
	m, err := refs(db, userID)
	if err != nil {
		return nil, err
	}
	for _, o := range occs {
		out = append(out, OccurrenceDTO{
			ID: o.ID, RuleID: o.RuleID, DueDate: jakarta.FormatDate(o.DueDate), Status: o.Status,
			Type: o.Type, Amount: o.Amount,
			Account: m.account(o.AccountID), ToAccount: m.toAccount(o.ToAccountID), Category: m.category(o.CategoryID),
			Note: o.Note, Tags: tagsOf(o.Tags), TransactionID: o.TransactionID,
		})
	}
	return out, nil
}

// Pending returns the user's pending occurrences (oldest first, limit 0 = all) and the total pending count.
func Pending(db *gorm.DB, userID uuid.UUID, limit int) ([]OccurrenceDTO, int64, error) {
	q := db.Model(&models.RecurringOccurrence{}).Where("user_id = ? AND status = ?", userID, models.OccPending)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var occs []models.RecurringOccurrence
	q = db.Where("user_id = ? AND status = ?", userID, models.OccPending).Order("due_date ASC, created_at ASC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if err := q.Find(&occs).Error; err != nil {
		return nil, 0, err
	}
	dtos, err := ToOccurrenceDTOs(db, userID, occs)
	return dtos, total, err
}
