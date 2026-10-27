package plan

import (
	"database/sql"
	"time"

	"beprod/internal/accounts"
	"beprod/internal/app"
	"beprod/internal/goals"
	"beprod/internal/jakarta"
	"beprod/internal/models"
	"beprod/internal/recurring"
	"beprod/internal/session"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func Register(api fiber.Router, d app.Deps) {
	api.Get("/plan", session.Require(d), get(d))
}

type safeDTO struct {
	PaydayDay  int    `json:"paydayDay"`
	NextPayday string `json:"nextPayday"`
	Days       int    `json:"days"`
	Spendable  int64  `json:"spendable"`
	CardDebt   int64  `json:"cardDebt"`
	Bills      int64  `json:"bills"`
	Goals      int64  `json:"goals"`
	Safe       int64  `json:"safe"`
	PerDay     *int64 `json:"perDay"`
}

type emergencyDTO struct {
	Have         int64    `json:"have"`
	MonthlySpend *int64   `json:"monthlySpend"`
	Months       *float64 `json:"months"`
	Level        *string  `json:"level"`
	NeedFor3     int64    `json:"needFor3"`
	NeedFor6     int64    `json:"needFor6"`
}

func get(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		today := jakarta.Today()
		var accs []models.Account
		if err := d.DB.Where("user_id = ? AND status = ?", u.ID, models.StatusActive).Find(&accs).Error; err != nil {
			return err
		}
		bal, err := accounts.Balances(d.DB, u.ID)
		if err != nil {
			return err
		}
		gs, err := goals.ActiveDTOs(d.DB, u.ID, today)
		if err != nil {
			return err
		}
		goalAcc := map[uuid.UUID]bool{}
		var goalUnsaved int64
		for _, g := range gs {
			goalAcc[g.AccountID] = true
			goalUnsaved += g.UnsavedThisMonth
		}
		var spendable, liquid, cardDebt int64
		for _, a := range accs {
			switch {
			case a.Type == models.AccountCreditCard:
				cardDebt += max(0, -bal[a.ID])
			default:
				liquid += bal[a.ID]
				if !goalAcc[a.ID] {
					spendable += bal[a.ID]
				}
			}
		}
		next := NextPayday(today, u.Payday)
		bills, err := upcomingBills(d.DB, u.ID, next, goalAcc)
		if err != nil {
			return err
		}
		safe := spendable - cardDebt - bills - goalUnsaved
		s := safeDTO{PaydayDay: u.Payday, NextPayday: jakarta.FormatDate(next), Days: DaysUntil(today, next),
			Spendable: spendable, CardDebt: cardDebt, Bills: bills, Goals: goalUnsaved, Safe: safe}
		if safe >= 0 {
			p := safe / int64(s.Days)
			s.PerDay = &p
		}
		em, err := emergency(d.DB, u.ID, today, liquid-cardDebt)
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"safeToSpend": s, "emergency": em})
	}
}

// upcomingBills sums expenses due on or before next: stored pending occurrences plus projected rule dates
// (each rule date once), skipping goal accounts.
func upcomingBills(db *gorm.DB, userID uuid.UUID, next time.Time, goalAcc map[uuid.UUID]bool) (int64, error) {
	var occs []models.RecurringOccurrence
	err := db.Where("user_id = ? AND status = ? AND type = ? AND due_date <= ?", userID, models.OccPending, models.TypeExpense, next).Find(&occs).Error
	if err != nil {
		return 0, err
	}
	var sum int64
	stored := map[uuid.UUID]map[time.Time]bool{}
	for _, o := range occs {
		if stored[o.RuleID] == nil {
			stored[o.RuleID] = map[time.Time]bool{}
		}
		stored[o.RuleID][o.DueDate] = true
		if !goalAcc[o.AccountID] {
			sum += o.Amount
		}
	}
	var rules []models.RecurringRule
	if err := db.Where("user_id = ? AND status = ? AND type = ?", userID, models.StatusActive, models.TypeExpense).Find(&rules).Error; err != nil {
		return 0, err
	}
	for _, r := range rules {
		if goalAcc[r.AccountID] {
			continue
		}
		for dt := r.NextDueDate; !dt.After(next) && (r.EndDate == nil || !dt.After(*r.EndDate)); dt = recurring.NextAfter(r.StartDate, r.Frequency, dt) {
			if !stored[r.ID][dt] {
				sum += r.Amount
			}
		}
	}
	return sum, nil
}

// emergency averages expenses over the last 3 full months (fewer if history is shorter).
// ponytail: zero average spend gives no ratio, so it is reported like "no history".
func emergency(db *gorm.DB, userID uuid.UUID, today time.Time, have int64) (emergencyDTO, error) {
	out := emergencyDTO{Have: have}
	var first sql.NullTime
	if err := db.Model(&models.Transaction{}).Where("user_id = ?", userID).Select("MIN(occurred_at)").Scan(&first).Error; err != nil {
		return out, err
	}
	if !first.Valid {
		return out, nil
	}
	thisMonth := jakarta.MonthStart(today)
	start := jakarta.MonthStart(jakarta.DateOf(first.Time))
	if floor := jakarta.AddMonths(thisMonth, -3); start.Before(floor) {
		start = floor
	}
	n := int64((thisMonth.Year()-start.Year())*12 + int(thisMonth.Month()-start.Month()))
	if n < 1 {
		return out, nil
	}
	var total int64
	err := db.Model(&models.Transaction{}).Where("user_id = ? AND type = ? AND occurred_at >= ? AND occurred_at < ?",
		userID, models.TypeExpense, jakarta.StartOf(start), jakarta.StartOf(thisMonth)).
		Select("COALESCE(SUM(amount), 0)").Scan(&total).Error
	if err != nil || total == 0 {
		return out, err
	}
	spend := (total + n - 1) / n
	tenths := max(0, have) * 10 / spend
	months := float64(tenths) / 10
	level := EmergencyLevel(tenths)
	out.MonthlySpend, out.Months, out.Level = &spend, &months, &level
	out.NeedFor3, out.NeedFor6 = max(0, 3*spend-have), max(0, 6*spend-have)
	return out, nil
}
