package budgets

import (
	"sort"
	"time"

	"beprod/internal/jakarta"
	"beprod/internal/models"
	"beprod/internal/transactions"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type View struct {
	ID              uuid.UUID                `json:"id"`
	Category        transactions.CategoryRef `json:"category"`
	MonthlyLimit    int64                    `json:"monthlyLimit"` // limit in effect in the selected month
	CurrentLimit    int64                    `json:"currentLimit"` // limit in effect in the current month
	RolloverEnabled bool                     `json:"rolloverEnabled"`
	Carryover       int64                    `json:"carryover"`
	Available       int64                    `json:"available"`
	Spent           int64                    `json:"spent"`
	Remaining       int64                    `json:"remaining"`
	PercentUsed     int64                    `json:"percentUsed"`
	Status          string                   `json:"status"`
}

type InactiveDTO struct {
	ID       uuid.UUID                `json:"id"`
	Category transactions.CategoryRef `json:"category"`
}

func CurrentMonth() time.Time { return jakarta.MonthStart(jakarta.Today()) }

func catRef(c models.Category) transactions.CategoryRef {
	return transactions.CategoryRef{ID: c.ID, Name: c.Name, Icon: c.Icon, Color: c.Color, Archived: c.Status == models.StatusArchived}
}

// ForMonth computes every active budget's numbers for month, replaying history from each budget's first month.
func ForMonth(db *gorm.DB, userID uuid.UUID, month time.Time) ([]View, error) {
	month = jakarta.MonthStart(month)
	current := CurrentMonth()

	var bs []models.Budget
	if err := db.Where("user_id = ? AND status = ?", userID, models.StatusActive).Find(&bs).Error; err != nil {
		return nil, err
	}
	if len(bs) == 0 {
		return []View{}, nil
	}
	catIDs := make([]string, 0, len(bs))
	budgetIDs := make([]string, 0, len(bs))
	for _, b := range bs {
		catIDs = append(catIDs, b.CategoryID.String())
		budgetIDs = append(budgetIDs, b.ID.String())
	}
	var catList []models.Category
	if err := db.Where("user_id = ? AND id IN ?", userID, catIDs).Find(&catList).Error; err != nil {
		return nil, err
	}
	cats := make(map[uuid.UUID]models.Category, len(catList))
	for _, c := range catList {
		cats[c.ID] = c
	}
	var limitList []models.BudgetLimit
	if err := db.Where("budget_id IN ?", budgetIDs).Find(&limitList).Error; err != nil {
		return nil, err
	}
	limitsOf := map[uuid.UUID][]models.BudgetLimit{}
	for _, l := range limitList {
		limitsOf[l.BudgetID] = append(limitsOf[l.BudgetID], l)
	}

	var included []models.Budget
	var inclCats []string
	firsts := map[uuid.UUID]time.Time{}
	for _, b := range bs {
		first := jakarta.MonthStart(jakarta.DateOf(b.CreatedAt))
		if first.After(month) {
			continue
		}
		firsts[b.ID] = first
		included = append(included, b)
		inclCats = append(inclCats, b.CategoryID.String())
	}
	if len(included) == 0 {
		return []View{}, nil
	}

	type spentRow struct {
		CategoryID uuid.UUID
		Month      string
		Total      int64
	}
	upper := jakarta.StartOf(jakarta.AddMonths(month, 1))
	var rows []spentRow
	err := db.Raw(`
		SELECT category_id,
		       to_char(date_trunc('month', occurred_at AT TIME ZONE 'Asia/Jakarta'), 'YYYY-MM') AS month,
		       SUM(amount)::bigint AS total
		FROM transactions
		WHERE user_id = ? AND type = 'expense' AND category_id IN ? AND occurred_at < ?
		GROUP BY 1, 2`, userID, inclCats, upper).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	spent := map[uuid.UUID]map[string]int64{}
	for _, r := range rows {
		if spent[r.CategoryID] == nil {
			spent[r.CategoryID] = map[string]int64{}
		}
		spent[r.CategoryID][r.Month] = r.Total
	}

	out := make([]View, 0, len(included))
	for _, b := range included {
		ls := limitsOf[b.ID]
		var earliest int64
		var earliestAt time.Time
		for i, l := range ls {
			if i == 0 || l.EffectiveMonth.Before(earliestAt) {
				earliest, earliestAt = l.MonthlyLimit, l.EffectiveMonth
			}
		}
		var inputs []MonthInput
		for m := firsts[b.ID]; !m.After(month); m = jakarta.AddMonths(m, 1) {
			limit, ok := LimitFor(limitsOf[b.ID], m)
			if !ok {
				limit = earliest
			}
			inputs = append(inputs, MonthInput{Month: m, Limit: limit, Spent: spent[b.CategoryID][jakarta.FormatMonth(m)]})
		}
		results := Compute(inputs, b.RolloverSince)
		sel := results[len(results)-1]
		cur, ok := LimitFor(limitsOf[b.ID], current)
		if !ok {
			cur = sel.Limit
		}
		out = append(out, View{
			ID: b.ID, Category: catRef(cats[b.CategoryID]), MonthlyLimit: sel.Limit, CurrentLimit: cur,
			RolloverEnabled: b.RolloverEnabled, Carryover: sel.Carryover, Available: sel.Available,
			Spent: sel.Spent, Remaining: sel.Remaining, PercentUsed: sel.PercentUsed, Status: sel.Status,
		})
	}
	order := map[uuid.UUID]int{}
	for _, b := range included {
		order[b.ID] = cats[b.CategoryID].SortOrder
	}
	sort.SliceStable(out, func(i, j int) bool {
		if oi, oj := order[out[i].ID], order[out[j].ID]; oi != oj {
			return oi < oj
		}
		return out[i].Category.Name < out[j].Category.Name
	})
	return out, nil
}

func Inactive(db *gorm.DB, userID uuid.UUID) ([]InactiveDTO, error) {
	var bs []models.Budget
	if err := db.Where("user_id = ? AND status = ?", userID, models.StatusInactive).Find(&bs).Error; err != nil {
		return nil, err
	}
	out := []InactiveDTO{}
	if len(bs) == 0 {
		return out, nil
	}
	ids := make([]string, 0, len(bs))
	for _, b := range bs {
		ids = append(ids, b.CategoryID.String())
	}
	var catList []models.Category
	if err := db.Where("user_id = ? AND id IN ?", userID, ids).Find(&catList).Error; err != nil {
		return nil, err
	}
	cats := make(map[uuid.UUID]models.Category, len(catList))
	for _, c := range catList {
		cats[c.ID] = c
	}
	for _, b := range bs {
		out = append(out, InactiveDTO{ID: b.ID, Category: catRef(cats[b.CategoryID])})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Category.Name < out[j].Category.Name })
	return out, nil
}
