package dashboard

import (
	"sort"
	"time"

	"beprod/internal/accounts"
	"beprod/internal/app"
	"beprod/internal/budgets"
	"beprod/internal/httpx"
	"beprod/internal/jakarta"
	"beprod/internal/models"
	"beprod/internal/recurring"
	"beprod/internal/session"
	"beprod/internal/transactions"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type balanceDTO struct {
	Total    int64          `json:"total"`
	Accounts []accounts.DTO `json:"accounts"`
}
type summaryDTO struct {
	Income  int64 `json:"income"`
	Expense int64 `json:"expense"`
	Net     int64 `json:"net"`
}
type breakdownItem struct {
	CategoryID *uuid.UUID `json:"categoryId"` // null untuk "Others"
	Name       string     `json:"name"`
	Icon       string     `json:"icon"`
	Color      string     `json:"color"`
	Amount     int64      `json:"amount"`
	Percent    int64      `json:"percent"`
}
type breakdownDTO struct {
	Total int64           `json:"total"`
	Items []breakdownItem `json:"items"`
}
type breakdownsDTO struct {
	Expense breakdownDTO `json:"expense"`
	Income  breakdownDTO `json:"income"`
}
type trendPoint struct {
	Month   string `json:"month"`
	Income  int64  `json:"income"`
	Expense int64  `json:"expense"`
}
type dueDTO struct {
	Items []recurring.OccurrenceDTO `json:"items"`
	Total int64                     `json:"total"`
}
type response struct {
	Month       string             `json:"month"`
	HasAccounts bool               `json:"hasAccounts"`
	Balance     balanceDTO         `json:"balance"`
	Summary     summaryDTO         `json:"summary"`
	SpentToday  int64              `json:"spentToday"` // always the real Jakarta today, whatever ?month= is
	Breakdown   breakdownsDTO      `json:"breakdown"`
	Trend       []trendPoint       `json:"trend"`
	Budgets     []budgets.View     `json:"budgets"`
	Due         dueDTO             `json:"due"`
	Recent      []transactions.DTO `json:"recent"`
}

type catRow struct {
	CategoryID        uuid.UUID
	Name, Icon, Color string
	Amount            int64
}

func Register(api fiber.Router, d app.Deps) {
	g := api.Group("/dashboard", session.Require(d))
	g.Get("/", handle(d))
}

func handle(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		month := jakarta.MonthStart(jakarta.Today())
		if q := c.Query("month"); q != "" {
			m, err := jakarta.ParseMonth(q)
			if err != nil {
				return httpx.Validation(map[string]string{"month": "Enter a valid month (YYYY-MM)"})
			}
			month = m
		}
		u := session.User(c)
		from := jakarta.StartOf(month)
		to := jakarta.StartOf(jakarta.AddMonths(month, 1))

		active, _, total, err := accounts.List(d.DB, u.ID)
		if err != nil {
			return err
		}
		if active == nil {
			active = []accounts.DTO{}
		}

		var sums []struct {
			Type  string
			Total int64
		}
		err = d.DB.Raw(`SELECT type, COALESCE(SUM(amount), 0)::bigint AS total
FROM transactions
WHERE user_id = ? AND type IN ('income','expense') AND occurred_at >= ? AND occurred_at < ?
GROUP BY type`, u.ID, from, to).Scan(&sums).Error
		if err != nil {
			return err
		}
		var sum summaryDTO
		for _, r := range sums {
			if r.Type == models.TypeIncome {
				sum.Income = r.Total
			} else {
				sum.Expense = r.Total
			}
		}
		sum.Net = sum.Income - sum.Expense

		today := jakarta.Today()
		var spentToday int64
		err = d.DB.Raw(`SELECT COALESCE(SUM(amount), 0)::bigint FROM transactions
WHERE user_id = ? AND type = 'expense' AND occurred_at >= ? AND occurred_at < ?`,
			u.ID, jakarta.StartOf(today), jakarta.StartOf(today.AddDate(0, 0, 1))).Scan(&spentToday).Error
		if err != nil {
			return err
		}

		expense, err := breakdown(d.DB, u.ID, models.TypeExpense, from, to)
		if err != nil {
			return err
		}
		income, err := breakdown(d.DB, u.ID, models.TypeIncome, from, to)
		if err != nil {
			return err
		}

		var trows []struct {
			Month string
			Type  string
			Total int64
		}
		err = d.DB.Raw(`SELECT to_char(date_trunc('month', occurred_at AT TIME ZONE 'Asia/Jakarta'), 'YYYY-MM') AS month,
       type, SUM(amount)::bigint AS total
FROM transactions
WHERE user_id = ? AND type IN ('income','expense') AND occurred_at >= ? AND occurred_at < ?
GROUP BY 1, 2`, u.ID, jakarta.StartOf(jakarta.AddMonths(month, -11)), to).Scan(&trows).Error
		if err != nil {
			return err
		}
		points := make([]trendPoint, 12)
		idx := make(map[string]int, 12)
		for i := 0; i < 12; i++ {
			m := jakarta.FormatMonth(jakarta.AddMonths(month, i-11))
			points[i] = trendPoint{Month: m}
			idx[m] = i
		}
		for _, r := range trows {
			i, ok := idx[r.Month]
			if !ok {
				continue
			}
			if r.Type == models.TypeIncome {
				points[i].Income = r.Total
			} else {
				points[i].Expense = r.Total
			}
		}

		bv, err := budgets.ForMonth(d.DB, u.ID, month)
		if err != nil {
			return err
		}
		sort.SliceStable(bv, func(i, j int) bool { return bv[i].PercentUsed > bv[j].PercentUsed })
		if len(bv) > 5 {
			bv = bv[:5]
		}
		if bv == nil {
			bv = []budgets.View{}
		}

		items, dueTotal, err := recurring.Pending(d.DB, u.ID, 5)
		if err != nil {
			return err
		}
		if items == nil {
			items = []recurring.OccurrenceDTO{}
		}

		var txns []models.Transaction
		err = d.DB.Where("user_id = ?", u.ID).Order("occurred_at DESC, created_at DESC").Limit(5).Find(&txns).Error
		if err != nil {
			return err
		}
		recent, err := transactions.ToDTOs(d.DB, u.ID, txns)
		if err != nil {
			return err
		}
		if recent == nil {
			recent = []transactions.DTO{}
		}

		return c.JSON(response{
			Month:       jakarta.FormatMonth(month),
			HasAccounts: len(active) > 0,
			Balance:     balanceDTO{Total: total, Accounts: active},
			Summary:     sum,
			SpentToday:  spentToday,
			Breakdown:   breakdownsDTO{Expense: expense, Income: income},
			Trend:       points,
			Budgets:     bv,
			Due:         dueDTO{Items: items, Total: dueTotal},
			Recent:      recent,
		})
	}
}

func breakdown(db *gorm.DB, userID uuid.UUID, typ string, from, to time.Time) (breakdownDTO, error) {
	var rows []catRow
	err := db.Raw(`SELECT t.category_id, c.name, c.icon, c.color, SUM(t.amount)::bigint AS amount
FROM transactions t
JOIN categories c ON c.id = t.category_id
WHERE t.user_id = ? AND t.type = ? AND t.occurred_at >= ? AND t.occurred_at < ?
GROUP BY t.category_id, c.name, c.icon, c.color
ORDER BY amount DESC, c.name ASC`, userID, typ, from, to).Scan(&rows).Error
	if err != nil {
		return breakdownDTO{}, err
	}
	return buildBreakdown(rows), nil
}

func buildBreakdown(rows []catRow) breakdownDTO {
	out := breakdownDTO{Items: []breakdownItem{}}
	var others int64
	for i, r := range rows {
		out.Total += r.Amount
		if i >= 6 {
			others += r.Amount
			continue
		}
		id := r.CategoryID
		out.Items = append(out.Items, breakdownItem{CategoryID: &id, Name: r.Name, Icon: r.Icon, Color: r.Color, Amount: r.Amount})
	}
	if len(rows) > 6 {
		out.Items = append(out.Items, breakdownItem{Name: "Others", Icon: "circle-dot", Color: "cyan", Amount: others})
	}
	for i := range out.Items {
		out.Items[i].Percent = out.Items[i].Amount * 100 / out.Total
	}
	return out
}
