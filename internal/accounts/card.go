package accounts

import (
	"time"

	"beprod/internal/jakarta"
	"beprod/internal/models"

	"gorm.io/gorm"
)

// CardDTO is the credit-card summary on an account (null for other types).
type CardDTO struct {
	Owed       int64   `json:"owed"`
	Available  *int64  `json:"available"`
	CycleSpend int64   `json:"cycleSpend"`
	Bill       *int64  `json:"bill"`
	DueDate    *string `json:"dueDate"`
	BillPaid   bool    `json:"billPaid"`
}

// ClampDay returns the civil date year-month-day, with day clamped to the month's last day.
func ClampDay(year int, month time.Month, day int) time.Time {
	if last := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day(); day > last {
		day = last
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// LastStatementDate is the latest statement date strictly before today (the statement closes at the end of its day).
func LastStatementDate(today time.Time, day int) time.Time {
	if s := ClampDay(today.Year(), today.Month(), day); s.Before(today) {
		return s
	}
	return ClampDay(today.Year(), today.Month()-1, day)
}

// DueDateAfter is the first dueDay date strictly after stmt.
func DueDateAfter(stmt time.Time, dueDay int) time.Time {
	if due := ClampDay(stmt.Year(), stmt.Month(), dueDay); due.After(stmt) {
		return due
	}
	return ClampDay(stmt.Year(), stmt.Month()+1, dueDay)
}

// BalanceAt is the account balance at the end of the civil date: opening (if opened by then) plus transactions before the next day.
func BalanceAt(db *gorm.DB, a models.Account, date time.Time) (int64, error) {
	var sum int64
	if !a.OpeningDate.After(date) {
		sum = a.OpeningBalance
	}
	var txns int64
	err := db.Raw(`SELECT (COALESCE((SELECT SUM(CASE WHEN type IN ('income','adjustment') THEN amount ELSE -amount END)
	    FROM transactions WHERE account_id = @id AND occurred_at < @end), 0)
	  + COALESCE((SELECT SUM(amount) FROM transactions
	    WHERE to_account_id = @id AND type = 'transfer' AND occurred_at < @end), 0))::bigint`,
		map[string]any{"id": a.ID, "end": jakarta.StartOf(date.AddDate(0, 0, 1))}).Scan(&txns).Error
	return sum + txns, err
}

// sumSince adds up amounts of the card's transactions of one kind with occurred_at >= since.
// kind: "expense" and "income" count rows on the card; "transfer" counts transfers into the card.
func sumSince(db *gorm.DB, a models.Account, kind string, since time.Time) (int64, error) {
	col := "account_id"
	if kind == "transfer" {
		col = "to_account_id"
	}
	var n int64
	err := db.Model(&models.Transaction{}).Where(col+" = ? AND type = ? AND occurred_at >= ?", a.ID, kind, since).
		Select("COALESCE(SUM(amount),0)").Scan(&n).Error
	return n, err
}

// CardInfo builds the card summary from the card's current balance. Formulas: PLAN-DESIGN.md 5.2.
func CardInfo(db *gorm.DB, a models.Account, balance int64, today time.Time) (CardDTO, error) {
	c := CardDTO{Owed: max(0, -balance)}
	if a.CreditLimit > 0 {
		avail := max(0, a.CreditLimit-c.Owed)
		c.Available = &avail
	}
	var err error
	if a.StatementDay == 0 {
		c.CycleSpend, err = sumSince(db, a, models.TypeExpense, jakarta.StartOf(jakarta.MonthStart(today)))
		return c, err
	}
	stmt := LastStatementDate(today, a.StatementDay)
	after := jakarta.StartOf(stmt.AddDate(0, 0, 1))
	if c.CycleSpend, err = sumSince(db, a, models.TypeExpense, after); err != nil {
		return c, err
	}
	var bill int64
	if !a.OpeningDate.After(stmt) {
		owedAt, err := BalanceAt(db, a, stmt)
		if err != nil {
			return c, err
		}
		in, err := sumSince(db, a, models.TypeTransfer, after)
		if err != nil {
			return c, err
		}
		inc, err := sumSince(db, a, models.TypeIncome, after)
		if err != nil {
			return c, err
		}
		bill = max(0, -owedAt-in-inc)
	}
	c.Bill, c.BillPaid = &bill, bill == 0
	if a.DueDay > 0 {
		due := jakarta.FormatDate(DueDateAfter(stmt, a.DueDay))
		c.DueDate = &due
	}
	return c, nil
}
