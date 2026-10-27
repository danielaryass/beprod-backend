package transactions

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"beprod/internal/models"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

const MaxAmount int64 = 999_999_999_999

type Fields struct {
	Type        string   `json:"type"`
	Amount      int64    `json:"amount"`
	AccountID   string   `json:"accountId"`
	ToAccountID string   `json:"toAccountId"`
	CategoryID  string   `json:"categoryId"`
	Note        string   `json:"note"`
	Tags        []string `json:"tags"`
}

type Resolved struct {
	Type        string
	Amount      int64
	AccountID   uuid.UUID
	ToAccountID *uuid.UUID
	CategoryID  *uuid.UUID
	Note        string
	Tags        pq.StringArray
}

var tagRe = regexp.MustCompile(`^[a-z0-9-]{1,20}$`)

const (
	msgTagChars = "Tags can only use letters, digits and hyphens (max 20 characters)"
	msgAccount  = "This account is no longer available"
)

func NormalizeTags(in []string) (pq.StringArray, string) {
	out := pq.StringArray{}
	seen := map[string]bool{}
	for _, t := range in {
		t = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(t), "#"))
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	for _, t := range out {
		if !tagRe.MatchString(t) {
			return pq.StringArray{}, msgTagChars
		}
	}
	if len(out) > 5 {
		return pq.StringArray{}, "You can add up to 5 tags"
	}
	return out, ""
}

func lookupAccount(db *gorm.DB, userID uuid.UUID, raw string, keepID *uuid.UUID) (uuid.UUID, bool, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, false, nil
	}
	var rows []models.Account
	if err := db.Where("id = ? AND user_id = ?", id, userID).Limit(1).Find(&rows).Error; err != nil {
		return uuid.Nil, false, err
	}
	if len(rows) == 0 {
		return uuid.Nil, false, nil
	}
	if rows[0].Status == models.StatusArchived && (keepID == nil || *keepID != id) {
		return uuid.Nil, false, nil
	}
	return id, true, nil
}

func lookupCategory(db *gorm.DB, userID uuid.UUID, raw string, keepID *uuid.UUID) (models.Category, bool, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return models.Category{}, false, nil
	}
	var rows []models.Category
	if err := db.Where("id = ? AND user_id = ?", id, userID).Limit(1).Find(&rows).Error; err != nil {
		return models.Category{}, false, err
	}
	if len(rows) == 0 {
		return models.Category{}, false, nil
	}
	if rows[0].Status == models.StatusArchived && (keepID == nil || *keepID != id) {
		return models.Category{}, false, nil
	}
	return rows[0], true, nil
}

// keep = values currently stored on the record being edited (nil on create); the same archived account/category is allowed (FR-5.10). err is DB errors only.
func ValidateFields(db *gorm.DB, userID uuid.UUID, f Fields, keep *Resolved) (Resolved, map[string]string, error) {
	fields := map[string]string{}
	r := Resolved{Type: f.Type, Amount: f.Amount}
	var keepAcc, keepTo, keepCat *uuid.UUID
	if keep != nil {
		keepAcc, keepTo, keepCat = &keep.AccountID, keep.ToAccountID, keep.CategoryID
	}

	validType := f.Type == models.TypeIncome || f.Type == models.TypeExpense || f.Type == models.TypeTransfer
	if !validType {
		fields["type"] = "Choose expense, income or transfer"
	}
	if f.Amount <= 0 {
		fields["amount"] = "Amount must be greater than 0"
	} else if f.Amount > MaxAmount {
		fields["amount"] = "Amount is too large"
	}

	accRaw := strings.TrimSpace(f.AccountID)
	if accRaw == "" {
		fields["accountId"] = "Select an account"
	} else {
		id, ok, err := lookupAccount(db, userID, accRaw, keepAcc)
		if err != nil {
			return Resolved{}, nil, err
		}
		if !ok {
			fields["accountId"] = msgAccount
		}
		r.AccountID = id
	}

	switch {
	case f.Type == models.TypeTransfer:
		toRaw := strings.TrimSpace(f.ToAccountID)
		switch {
		case toRaw == "":
			fields["toAccountId"] = "Select a destination account"
		case strings.EqualFold(accRaw, toRaw):
			fields["toAccountId"] = "Choose two different accounts."
		default:
			id, ok, err := lookupAccount(db, userID, toRaw, keepTo)
			if err != nil {
				return Resolved{}, nil, err
			}
			if !ok {
				fields["toAccountId"] = msgAccount
			}
			r.ToAccountID = &id
		}
	case validType:
		catRaw := strings.TrimSpace(f.CategoryID)
		if catRaw == "" {
			fields["categoryId"] = "Select a category"
			break
		}
		cat, ok, err := lookupCategory(db, userID, catRaw, keepCat)
		if err != nil {
			return Resolved{}, nil, err
		}
		switch {
		case !ok:
			fields["categoryId"] = "This category is no longer available"
		case cat.Type != f.Type:
			fields["categoryId"] = "Category doesn't match the transaction type"
		default:
			r.CategoryID = &cat.ID
		}
	}

	r.Note = strings.TrimSpace(f.Note)
	if utf8.RuneCountInString(r.Note) > 200 {
		fields["note"] = "Note can be at most 200 characters"
	}
	tags, msg := NormalizeTags(f.Tags)
	if msg != "" {
		fields["tags"] = msg
	}
	r.Tags = tags

	if len(fields) > 0 {
		return Resolved{}, fields, nil
	}
	return r, map[string]string{}, nil
}
