package transactions

import (
	"strconv"
	"strings"

	"beprod/internal/app"
	"beprod/internal/httpx"
	"beprod/internal/jakarta"
	"beprod/internal/models"
	"beprod/internal/session"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type DayGroup struct {
	Date         string `json:"date"`
	NetTotal     int64  `json:"netTotal"`
	Transactions []DTO  `json:"transactions"`
}

type ListResponse struct {
	Days    []DayGroup `json:"days"`
	Page    int        `json:"page"`
	HasMore bool       `json:"hasMore"`
}

const pageSize = 50

// escapeLike escapes \, % and _ so user input is matched literally by LIKE/ILIKE (default escape char is backslash).
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// cleanTag lower-cases, trims spaces and one leading '#'.
func cleanTag(s string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "#"))
}

func listHandler(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)

		var month = jakarta.MonthStart(jakarta.Today())
		if m := c.Query("month"); m != "" {
			var err error
			if month, err = jakarta.ParseMonth(m); err != nil {
				return httpx.Validation(map[string]string{"month": "Enter a valid month (YYYY-MM)"})
			}
		}

		typ := c.Query("type")
		if typ != "" && typ != models.TypeIncome && typ != models.TypeExpense && typ != models.TypeTransfer {
			return httpx.Validation(map[string]string{"type": "Choose expense, income or transfer"})
		}

		page, err := strconv.Atoi(c.Query("page"))
		if err != nil || page < 1 {
			page = 1
		}
		empty := ListResponse{Days: []DayGroup{}, Page: page, HasMore: false}

		var accID, catID uuid.UUID
		if s := c.Query("accountId"); s != "" {
			if accID, err = uuid.Parse(s); err != nil {
				return c.JSON(empty)
			}
		}
		if s := c.Query("categoryId"); s != "" {
			if catID, err = uuid.Parse(s); err != nil {
				return c.JSON(empty)
			}
		}
		tag := cleanTag(c.Query("tag"))
		q := strings.TrimSpace(c.Query("q"))

		where := "user_id = ? AND occurred_at >= ? AND occurred_at < ?"
		args := []any{u.ID, jakarta.StartOf(month), jakarta.StartOf(jakarta.AddMonths(month, 1))}
		if typ != "" {
			where += " AND type = ?"
			args = append(args, typ)
		}
		if accID != uuid.Nil {
			where += " AND (account_id = ? OR to_account_id = ?)"
			args = append(args, accID, accID)
		}
		if catID != uuid.Nil {
			where += " AND category_id = ?"
			args = append(args, catID)
		}
		if tag != "" {
			where += " AND ? = ANY(tags)"
			args = append(args, tag)
		}
		if q != "" {
			where += " AND note ILIKE '%' || ? || '%'"
			args = append(args, escapeLike(q))
		}

		var rows []models.Transaction
		if err := d.DB.Where(where, args...).
			Order("occurred_at DESC, created_at DESC, id DESC").
			Offset((page - 1) * pageSize).Limit(pageSize + 1).Find(&rows).Error; err != nil {
			return err
		}
		hasMore := len(rows) > pageSize
		if hasMore {
			rows = rows[:pageSize]
		}
		if len(rows) == 0 {
			return c.JSON(empty)
		}

		dtos, err := ToDTOs(d.DB, u.ID, rows)
		if err != nil {
			return err
		}
		var groups []DayGroup
		for i, r := range rows {
			day := r.OccurredAt.In(jakarta.Loc).Format("2006-01-02")
			if i == 0 || groups[len(groups)-1].Date != day {
				groups = append(groups, DayGroup{Date: day, Transactions: []DTO{}})
			}
			g := &groups[len(groups)-1]
			g.Transactions = append(g.Transactions, dtos[i])
		}

		maxDay, err := jakarta.ParseDate(groups[0].Date)
		if err != nil {
			return err
		}
		minDay, err := jakarta.ParseDate(groups[len(groups)-1].Date)
		if err != nil {
			return err
		}

		var caseArgs []any
		caseSQL := "CASE WHEN type = 'income' THEN amount WHEN type = 'expense' THEN -amount ELSE 0 END"
		if accID != uuid.Nil {
			caseSQL = "CASE WHEN type = 'income' THEN amount WHEN type = 'expense' THEN -amount " +
				"WHEN type = 'transfer' AND account_id = ? THEN -amount " +
				"WHEN type = 'transfer' AND to_account_id = ? THEN amount ELSE 0 END"
			caseArgs = []any{accID, accID}
		}
		sql := "SELECT to_char(occurred_at AT TIME ZONE 'Asia/Jakarta', 'YYYY-MM-DD') AS day, " +
			"COALESCE(SUM(" + caseSQL + "), 0)::bigint AS net " +
			"FROM transactions WHERE " + where + " AND occurred_at >= ? AND occurred_at < ? GROUP BY 1"
		allArgs := append(append(caseArgs, args...), jakarta.StartOf(minDay), jakarta.StartOf(maxDay.AddDate(0, 0, 1)))

		var nets []struct {
			Day string `gorm:"column:day"`
			Net int64  `gorm:"column:net"`
		}
		if err := d.DB.Raw(sql, allArgs...).Scan(&nets).Error; err != nil {
			return err
		}
		byDay := make(map[string]int64, len(nets))
		for _, n := range nets {
			byDay[n.Day] = n.Net
		}
		for i := range groups {
			groups[i].NetTotal = byDay[groups[i].Date]
		}

		return c.JSON(ListResponse{Days: groups, Page: page, HasMore: hasMore})
	}
}

func tagsHandler(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		q := cleanTag(c.Query("q"))
		var tags []string
		if err := d.DB.Raw(`SELECT DISTINCT tag COLLATE "C" AS tag FROM (SELECT unnest(tags) AS tag FROM transactions WHERE user_id = ?) s
			WHERE tag LIKE ? ORDER BY tag LIMIT 10`, u.ID, escapeLike(q)+"%").Scan(&tags).Error; err != nil {
			return err
		}
		if tags == nil {
			tags = []string{}
		}
		return c.JSON(fiber.Map{"tags": tags})
	}
}
