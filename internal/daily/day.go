package daily

import (
	"strings"
	"time"
	"unicode/utf8"

	"beprod/internal/app"
	"beprod/internal/httpx"
	"beprod/internal/jakarta"
	"beprod/internal/models"
	"beprod/internal/session"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DailyHabitDTO is a habit with one day's progress.
type DailyHabitDTO struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Kind   string    `json:"kind"`
	Target int       `json:"target"`
	Unit   string    `json:"unit"`
	Value  int       `json:"value"`
	Done   bool      `json:"done"`
	Streak int       `json:"streak"`
}

// DailyPromptDTO is a note prompt with one day's answer (nulls when unanswered).
type DailyPromptDTO struct {
	ID     uuid.UUID `json:"id"`
	Label  string    `json:"label"`
	Kind   string    `json:"kind"`
	Text   *string   `json:"text"`
	Rating *int      `json:"rating"`
}

var errBadDate = httpx.Validation(map[string]string{"date": "Pick today or an earlier day"})

// parseDay parses s (empty means today); bad formats and future days are errors.
func parseDay(s string) (time.Time, error) {
	today := jakarta.Today()
	if s == "" {
		return today, nil
	}
	date, err := jakarta.ParseDate(s)
	if err != nil || date.After(today) {
		return time.Time{}, errBadDate
	}
	return date, nil
}

type habitLogs map[uuid.UUID]map[time.Time]int

func (l habitLogs) value(id uuid.UUID, date time.Time) int { return l[id][date] }

// loadLogs fetches the user's logs for the streak window ending at date in one query,
// optionally restricted to one habit.
func loadLogs(db *gorm.DB, userID uuid.UUID, date time.Time, habitID *uuid.UUID) (habitLogs, error) {
	q := db.Where("user_id = ? AND date BETWEEN ? AND ?", userID, date.AddDate(0, 0, -365), date)
	if habitID != nil {
		q = q.Where("habit_id = ?", *habitID)
	}
	var rows []models.HabitLog
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := habitLogs{}
	for _, r := range rows {
		if out[r.HabitID] == nil {
			out[r.HabitID] = map[time.Time]int{}
		}
		out[r.HabitID][jakarta.DateOf(r.Date)] = r.Value
	}
	return out, nil
}

func isDone(h models.Habit, v int) bool {
	if h.Kind == models.HabitCheck {
		return v >= 1
	}
	return v >= h.Target
}

func dailyHabit(h models.Habit, logs habitLogs, date time.Time) DailyHabitDTO {
	v := logs.value(h.ID, date)
	streak := Streak(Weekdays(h.Days), jakarta.DateOf(h.CreatedAt), date, func(d time.Time) bool {
		return isDone(h, logs.value(h.ID, d))
	})
	return DailyHabitDTO{ID: h.ID, Name: h.Name, Kind: h.Kind, Target: h.Target, Unit: h.Unit,
		Value: v, Done: isDone(h, v), Streak: streak}
}

func dailyPrompt(p models.NotePrompt, e *models.NoteEntry) DailyPromptDTO {
	dto := DailyPromptDTO{ID: p.ID, Label: p.Label, Kind: p.Kind}
	if e == nil {
		return dto
	}
	if p.Kind == models.PromptText {
		dto.Text = &e.Text
	} else {
		dto.Rating = e.Rating
	}
	return dto
}

func getDaily(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		date, err := parseDay(c.Query("date"))
		if err != nil {
			return err
		}
		var habits []models.Habit
		if err := d.DB.Where("user_id = ? AND NOT hidden AND created_at < ?", u.ID, date.AddDate(0, 0, 1)).
			Order("sort_order ASC, created_at ASC").Find(&habits).Error; err != nil {
			return err
		}
		logs, err := loadLogs(d.DB, u.ID, date, nil)
		if err != nil {
			return err
		}
		hs := []DailyHabitDTO{}
		for _, h := range habits {
			// created_at is a timestamp; compare civil dates exactly.
			if Weekdays(h.Days).Has(date) && !jakarta.DateOf(h.CreatedAt).After(date) {
				hs = append(hs, dailyHabit(h, logs, date))
			}
		}
		var prompts []models.NotePrompt
		if err := d.DB.Where("user_id = ? AND NOT hidden", u.ID).Order("sort_order ASC, created_at ASC").Find(&prompts).Error; err != nil {
			return err
		}
		var entries []models.NoteEntry
		if err := d.DB.Where("user_id = ? AND date = ?", u.ID, date).Find(&entries).Error; err != nil {
			return err
		}
		byPrompt := map[uuid.UUID]*models.NoteEntry{}
		for i := range entries {
			byPrompt[entries[i].PromptID] = &entries[i]
		}
		ps := []DailyPromptDTO{}
		for _, p := range prompts {
			ps = append(ps, dailyPrompt(p, byPrompt[p.ID]))
		}
		return c.JSON(fiber.Map{"date": jakarta.FormatDate(date), "habits": hs, "prompts": ps})
	}
}

func putHabitLog(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		date, err := parseDay(c.Params("date"))
		if err != nil {
			return err
		}
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		var in struct {
			Value *int `json:"value"`
		}
		if err := httpx.Bind(c, &in); err != nil {
			return err
		}
		h, err := loadHabit(d.DB, u.ID, id)
		if err != nil {
			return err
		}
		if h.Kind == models.HabitCheck && (in.Value == nil || *in.Value < 0 || *in.Value > 1) {
			return httpx.Validation(map[string]string{"value": "Value must be 0 or 1"})
		}
		if h.Kind == models.HabitCount && (in.Value == nil || *in.Value < 0 || *in.Value > 9999) {
			return httpx.Validation(map[string]string{"value": "Value must be 0–9999"})
		}
		if *in.Value == 0 {
			err = d.DB.Where("habit_id = ? AND user_id = ? AND date = ?", h.ID, u.ID, date).Delete(&models.HabitLog{}).Error
		} else {
			err = d.DB.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "habit_id"}, {Name: "date"}},
				DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
			}).Create(&models.HabitLog{UserID: u.ID, HabitID: h.ID, Date: date, Value: *in.Value}).Error
		}
		if err != nil {
			return err
		}
		logs, err := loadLogs(d.DB, u.ID, date, &h.ID)
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"habit": dailyHabit(h, logs, date)})
	}
}

func putNote(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u := session.User(c)
		date, err := parseDay(c.Params("date"))
		if err != nil {
			return err
		}
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		var in struct {
			Text   *string `json:"text"`
			Rating *int    `json:"rating"`
		}
		if err := httpx.Bind(c, &in); err != nil {
			return err
		}
		p, err := loadPrompt(d.DB, u.ID, id)
		if err != nil {
			return err
		}
		entry := models.NoteEntry{UserID: u.ID, PromptID: p.ID, Date: date}
		empty := false
		cols := []string{"text", "rating", "updated_at"}
		if p.Kind == models.PromptText {
			text := ""
			if in.Text != nil {
				text = strings.TrimSpace(*in.Text)
			}
			if utf8.RuneCountInString(text) > 2000 {
				return httpx.Validation(map[string]string{"text": "Keep it under 2000 characters"})
			}
			entry.Text, empty = text, text == ""
		} else {
			if in.Rating != nil && (*in.Rating < 1 || *in.Rating > 5) {
				return httpx.Validation(map[string]string{"rating": "Pick 1–5"})
			}
			entry.Rating, empty = in.Rating, in.Rating == nil
		}
		if empty {
			err = d.DB.Where("prompt_id = ? AND user_id = ? AND date = ?", p.ID, u.ID, date).Delete(&models.NoteEntry{}).Error
		} else {
			err = d.DB.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "prompt_id"}, {Name: "date"}},
				DoUpdates: clause.AssignmentColumns(cols),
			}).Create(&entry).Error
		}
		if err != nil {
			return err
		}
		if empty {
			return c.JSON(fiber.Map{"prompt": dailyPrompt(p, nil)})
		}
		return c.JSON(fiber.Map{"prompt": dailyPrompt(p, &entry)})
	}
}
