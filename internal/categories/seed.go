package categories

import (
	"github.com/google/uuid"
	"gorm.io/gorm"

	"beprod/internal/models"
)

var Colors = []string{"red", "orange", "amber", "yellow", "lime", "green", "teal", "cyan", "blue", "indigo", "purple", "pink"}
var Icons = []string{"utensils", "car", "shopping-bag", "receipt", "home", "heart-pulse", "film", "graduation-cap", "circle-dot",
	"briefcase", "star", "laptop", "gift", "coins", "coffee", "plane", "phone", "shirt", "baby", "paw-print", "dumbbell", "wallet", "piggy-bank", "tag"}

type categoryDef struct {
	Name string
	Icon string
	Col  string
	Type string
}

var defaultExpense = []categoryDef{
	{"Food & Drinks", "utensils", "orange", models.TypeExpense},
	{"Transport", "car", "blue", models.TypeExpense},
	{"Shopping", "shopping-bag", "pink", models.TypeExpense},
	{"Bills & Utilities", "receipt", "yellow", models.TypeExpense},
	{"Housing", "home", "indigo", models.TypeExpense},
	{"Health", "heart-pulse", "red", models.TypeExpense},
	{"Entertainment", "film", "purple", models.TypeExpense},
	{"Education", "graduation-cap", "teal", models.TypeExpense},
	{"Other Expense", "circle-dot", "cyan", models.TypeExpense},
}

var defaultIncome = []categoryDef{
	{"Salary", "briefcase", "green", models.TypeIncome},
	{"Bonus", "star", "lime", models.TypeIncome},
	{"Freelance", "laptop", "teal", models.TypeIncome},
	{"Gift", "gift", "pink", models.TypeIncome},
	{"Other Income", "coins", "amber", models.TypeIncome},
}

// SeedDefaults inserts the 14 default categories for userID using tx (all status active), sort_order 1..N per type.
func SeedDefaults(tx *gorm.DB, userID uuid.UUID) error {
	rows := make([]models.Category, 0, len(defaultExpense)+len(defaultIncome))
	for i, d := range defaultExpense {
		rows = append(rows, models.Category{
			UserID:    userID,
			Name:      d.Name,
			Type:      d.Type,
			Icon:      d.Icon,
			Color:     d.Col,
			SortOrder: i + 1,
			Status:    models.StatusActive,
		})
	}
	for i, d := range defaultIncome {
		rows = append(rows, models.Category{
			UserID:    userID,
			Name:      d.Name,
			Type:      d.Type,
			Icon:      d.Icon,
			Color:     d.Col,
			SortOrder: i + 1,
			Status:    models.StatusActive,
		})
	}
	return tx.Create(&rows).Error
}
