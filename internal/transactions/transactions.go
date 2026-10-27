package transactions

import (
	"time"

	"beprod/internal/jakarta"
	"beprod/internal/models"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

type AccountRef struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Type     string    `json:"type"`
	Archived bool      `json:"archived"`
}

type CategoryRef struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Icon     string    `json:"icon"`
	Color    string    `json:"color"`
	Archived bool      `json:"archived"`
}

type DTO struct {
	ID         uuid.UUID    `json:"id"`
	Type       string       `json:"type"`
	Amount     int64        `json:"amount"`
	OccurredAt string       `json:"occurredAt"`
	Date       string       `json:"date"`
	Time       string       `json:"time"`
	Account    AccountRef   `json:"account"`
	ToAccount  *AccountRef  `json:"toAccount"`
	Category   *CategoryRef `json:"category"`
	Note       string       `json:"note"`
	Tags       []string     `json:"tags"`
	Source     string       `json:"source"`
	CreatedAt  time.Time    `json:"createdAt"`
	UpdatedAt  time.Time    `json:"updatedAt"`
}

// Insert returns the GORM error unwrapped so callers can check errors.Is(err, gorm.ErrDuplicatedKey).
func Insert(tx *gorm.DB, userID uuid.UUID, r Resolved, occurredAt time.Time, source string, occurrenceID *uuid.UUID, idemKey *string) (models.Transaction, error) {
	tags := r.Tags
	if tags == nil {
		tags = pq.StringArray{}
	}
	t := models.Transaction{
		UserID: userID, Type: r.Type, Amount: r.Amount, OccurredAt: occurredAt,
		AccountID: r.AccountID, ToAccountID: r.ToAccountID, CategoryID: r.CategoryID,
		Note: r.Note, Tags: tags, Source: source,
		RecurringOccurrenceID: occurrenceID, IdempotencyKey: idemKey,
	}
	err := tx.Create(&t).Error
	return t, err
}

func ToDTOs(db *gorm.DB, userID uuid.UUID, txns []models.Transaction) ([]DTO, error) {
	if len(txns) == 0 {
		return []DTO{}, nil
	}
	var accs []models.Account
	if err := db.Where("user_id = ?", userID).Find(&accs).Error; err != nil {
		return nil, err
	}
	var cats []models.Category
	if err := db.Where("user_id = ?", userID).Find(&cats).Error; err != nil {
		return nil, err
	}
	accByID := make(map[uuid.UUID]models.Account, len(accs))
	for _, a := range accs {
		accByID[a.ID] = a
	}
	catByID := make(map[uuid.UUID]models.Category, len(cats))
	for _, c := range cats {
		catByID[c.ID] = c
	}
	accRef := func(id uuid.UUID) *AccountRef {
		a, ok := accByID[id]
		if !ok {
			return &AccountRef{ID: id}
		}
		return &AccountRef{ID: a.ID, Name: a.Name, Type: a.Type, Archived: a.Status == models.StatusArchived}
	}

	out := make([]DTO, 0, len(txns))
	for _, t := range txns {
		at := t.OccurredAt.In(jakarta.Loc)
		d := DTO{
			ID: t.ID, Type: t.Type, Amount: t.Amount,
			OccurredAt: at.Format(time.RFC3339), Date: at.Format("2006-01-02"), Time: at.Format("15:04"),
			Account: *accRef(t.AccountID), Note: t.Note, Tags: []string(t.Tags), Source: t.Source,
			CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
		}
		if d.Tags == nil {
			d.Tags = []string{}
		}
		if t.ToAccountID != nil {
			d.ToAccount = accRef(*t.ToAccountID)
		}
		if t.CategoryID != nil {
			ref := &CategoryRef{ID: *t.CategoryID}
			if c, ok := catByID[*t.CategoryID]; ok {
				ref = &CategoryRef{ID: c.ID, Name: c.Name, Icon: c.Icon, Color: c.Color, Archived: c.Status == models.StatusArchived}
			}
			d.Category = ref
		}
		out = append(out, d)
	}
	return out, nil
}
