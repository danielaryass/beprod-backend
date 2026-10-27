package accounts

import (
	"strings"
	"unicode/utf8"

	"beprod/internal/httpx"
	"beprod/internal/jakarta"
	"beprod/internal/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type DTO struct {
	ID              uuid.UUID `json:"id"`
	Name            string    `json:"name"`
	Type            string    `json:"type"`
	OpeningBalance  int64     `json:"openingBalance"`
	OpeningDate     string    `json:"openingDate"`
	SortOrder       int       `json:"sortOrder"`
	Status          string    `json:"status"`
	Balance         int64     `json:"balance"`
	Owed            bool      `json:"owed"`
	CreditLimit     int64     `json:"creditLimit"`
	StatementDay    int       `json:"statementDay"`
	DueDay          int       `json:"dueDay"`
	Card            *CardDTO  `json:"card"`
	HasTransactions bool      `json:"hasTransactions"`
}

func ToDTO(a models.Account, balance int64) DTO {
	return DTO{
		ID: a.ID, Name: a.Name, Type: a.Type, OpeningBalance: a.OpeningBalance,
		OpeningDate: jakarta.FormatDate(a.OpeningDate), SortOrder: a.SortOrder, Status: a.Status,
		Balance: balance, Owed: a.Type == models.AccountCreditCard && balance < 0,
		CreditLimit: a.CreditLimit, StatementDay: a.StatementDay, DueDay: a.DueDay,
	}
}

// ToDTOCard is ToDTO plus the card summary for credit cards.
func ToDTOCard(db *gorm.DB, a models.Account, balance int64) (DTO, error) {
	dto := ToDTO(a, balance)
	var err error
	if dto.HasTransactions, err = hasTransactions(db, a.ID); err != nil {
		return dto, err
	}
	if a.Type != models.AccountCreditCard {
		return dto, nil
	}
	c, err := CardInfo(db, a, balance, jakarta.Today())
	if err != nil {
		return dto, err
	}
	dto.Card = &c
	return dto, nil
}

// hasTransactions reports whether any transaction uses the account as source or destination.
func hasTransactions(db *gorm.DB, id uuid.UUID) (bool, error) {
	var n int64
	err := db.Model(&models.Transaction{}).Where("account_id = ? OR to_account_id = ?", id, id).Limit(1).Count(&n).Error
	return n > 0, err
}

const balanceSQL = `SELECT a.id AS id,
  (a.opening_balance
  + COALESCE((SELECT SUM(CASE WHEN t.type IN ('income','adjustment') THEN t.amount ELSE -t.amount END)
              FROM transactions t WHERE t.account_id = a.id), 0)
  + COALESCE((SELECT SUM(t.amount) FROM transactions t
              WHERE t.to_account_id = a.id AND t.type = 'transfer'), 0))::bigint AS balance
FROM accounts a WHERE a.user_id = ?`

// Balances returns balance per account id for all of the user's accounts (active + archived). FR-3.3.
func Balances(db *gorm.DB, userID uuid.UUID) (map[uuid.UUID]int64, error) {
	var rows []struct {
		ID      uuid.UUID `gorm:"column:id"`
		Balance int64     `gorm:"column:balance"`
	}
	if err := db.Raw(balanceSQL, userID).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]int64, len(rows))
	for _, r := range rows {
		out[r.ID] = r.Balance
	}
	return out, nil
}

// List returns active accounts (sort_order, created_at), archived accounts (name), and total balance of active accounts.
func List(db *gorm.DB, userID uuid.UUID) (active, archived []DTO, total int64, err error) {
	bal, err := Balances(db, userID)
	if err != nil {
		return nil, nil, 0, err
	}
	var act, arc []models.Account
	if err = db.Where("user_id = ? AND status = ?", userID, models.StatusActive).Order("sort_order ASC, created_at ASC").Find(&act).Error; err != nil {
		return nil, nil, 0, err
	}
	if err = db.Where("user_id = ? AND status = ?", userID, models.StatusArchived).Order("name ASC").Find(&arc).Error; err != nil {
		return nil, nil, 0, err
	}
	active, archived = []DTO{}, []DTO{}
	for _, a := range act {
		dto, err := ToDTOCard(db, a, bal[a.ID])
		if err != nil {
			return nil, nil, 0, err
		}
		active = append(active, dto)
		total += bal[a.ID]
	}
	for _, a := range arc {
		dto, err := ToDTOCard(db, a, bal[a.ID])
		if err != nil {
			return nil, nil, 0, err
		}
		archived = append(archived, dto)
	}
	return active, archived, total, nil
}

// loadOwned returns the account with this id owned by userID, or httpx.NotFound().
func loadOwned(db *gorm.DB, userID, id uuid.UUID) (models.Account, error) {
	var rows []models.Account
	if err := db.Where("id = ? AND user_id = ?", id, userID).Limit(1).Find(&rows).Error; err != nil {
		return models.Account{}, err
	}
	if len(rows) == 0 {
		return models.Account{}, httpx.NotFound()
	}
	return rows[0], nil
}

const maxBalance int64 = 999_999_999_999
const maxActiveAccounts = 50

type input struct {
	Name           *string `json:"name"`
	Type           *string `json:"type"`
	OpeningBalance *int64  `json:"openingBalance"`
	OpeningDate    *string `json:"openingDate"`
	CreditLimit    *int64  `json:"creditLimit"`
	StatementDay   *int    `json:"statementDay"`
	DueDay         *int    `json:"dueDay"`
}

// applyInput validates in and writes valid values into *a. create=true: missing name/type are errors and a
// missing/empty openingDate defaults to jakarta.Today(). Returns field errors (len 0 = ok).
func applyInput(a *models.Account, in input, create bool) map[string]string {
	fields := map[string]string{}
	if in.Name != nil || create {
		name := ""
		if in.Name != nil {
			name = strings.TrimSpace(*in.Name)
		}
		if n := utf8.RuneCountInString(name); n < 1 || n > 40 {
			fields["name"] = "Name must be 1–40 characters"
		} else {
			a.Name = name
		}
	}
	if in.Type != nil || create {
		typ := ""
		if in.Type != nil {
			typ = *in.Type
		}
		switch typ {
		case models.AccountCash, models.AccountBank, models.AccountEwallet, models.AccountCreditCard:
			a.Type = typ
		default:
			fields["type"] = "Choose a valid account type"
		}
	}
	if in.OpeningBalance != nil {
		if b := *in.OpeningBalance; b < -maxBalance || b > maxBalance {
			fields["openingBalance"] = "Opening balance is out of range"
		} else {
			a.OpeningBalance = b
		}
	}
	if in.CreditLimit != nil {
		if v := *in.CreditLimit; v < 0 || v > maxBalance {
			fields["creditLimit"] = "Credit limit is out of range"
		} else {
			a.CreditLimit = v
		}
	}
	for _, f := range []struct {
		name string
		in   *int
		dst  *int
	}{{"statementDay", in.StatementDay, &a.StatementDay}, {"dueDay", in.DueDay, &a.DueDay}} {
		if f.in == nil {
			continue
		}
		if v := *f.in; v < 0 || v > 31 {
			fields[f.name] = "Choose a day from 1 to 31, or leave empty"
		} else {
			*f.dst = v
		}
	}
	if create && (in.OpeningDate == nil || *in.OpeningDate == "") {
		a.OpeningDate = jakarta.Today()
	} else if in.OpeningDate != nil {
		d, err := jakarta.ParseDate(*in.OpeningDate)
		switch {
		case err != nil:
			fields["openingDate"] = "Enter a valid date"
		case d.After(jakarta.Today()):
			fields["openingDate"] = "Opening date can't be in the future"
		default:
			a.OpeningDate = d
		}
	}
	return fields
}
