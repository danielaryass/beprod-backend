package categories

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"beprod/internal/httpx"
	"beprod/internal/models"
)

type DTO struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Icon      string    `json:"icon"`
	Color     string    `json:"color"`
	SortOrder int       `json:"sortOrder"`
	Status    string    `json:"status"`
}

func ToDTO(c models.Category) DTO {
	return DTO{ID: c.ID, Name: c.Name, Type: c.Type, Icon: c.Icon, Color: c.Color, SortOrder: c.SortOrder, Status: c.Status}
}

func toDTOs(cs []models.Category) []DTO {
	out := make([]DTO, 0, len(cs))
	for _, c := range cs {
		out = append(out, ToDTO(c))
	}
	return out
}

const maxActivePerType = 100

// loadOwned returns the category with this id owned by userID, or httpx.NotFound().
func loadOwned(db *gorm.DB, userID, id uuid.UUID) (models.Category, error) {
	var cs []models.Category
	if err := db.Where("id = ? AND user_id = ?", id, userID).Limit(1).Find(&cs).Error; err != nil {
		return models.Category{}, err
	}
	if len(cs) == 0 {
		return models.Category{}, httpx.NotFound()
	}
	return cs[0], nil
}

func validateName(raw string) (clean, msg string) {
	clean = strings.TrimSpace(raw)
	if n := utf8.RuneCountInString(clean); n < 1 || n > 30 {
		return clean, "Name must be 1–30 characters"
	}
	return clean, ""
}

func validateIcon(v string) string {
	if slices.Contains(Icons, v) {
		return ""
	}
	return "Choose an icon"
}

func validateColor(v string) string {
	if slices.Contains(Colors, v) {
		return ""
	}
	return "Choose a colour"
}

func validType(v string) bool { return v == models.TypeIncome || v == models.TypeExpense }
