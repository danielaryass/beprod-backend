package categories_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"beprod/internal/categories"
	"beprod/internal/models"
	"beprod/internal/testutil"
)

func TestSeedDefaults_Creates14(t *testing.T) {
	g := testutil.DB(t)
	uid := uuid.New()
	if err := categories.SeedDefaults(g, uid); err != nil {
		t.Fatalf("SeedDefaults: %v", err)
	}
	var count int64
	g.Model(&models.Category{}).Where("user_id = ?", uid).Count(&count)
	if count != 14 {
		t.Errorf("want 14 rows, got %d", count)
	}
	var expCount, incCount int64
	g.Model(&models.Category{}).Where("user_id = ? AND type = ?", uid, models.TypeExpense).Count(&expCount)
	g.Model(&models.Category{}).Where("user_id = ? AND type = ?", uid, models.TypeIncome).Count(&incCount)
	if expCount != 9 {
		t.Errorf("want 9 expense, got %d", expCount)
	}
	if incCount != 5 {
		t.Errorf("want 5 income, got %d", incCount)
	}
	var activeCount int64
	g.Model(&models.Category{}).Where("user_id = ? AND status = ?", uid, "active").Count(&activeCount)
	if activeCount != 14 {
		t.Errorf("want all 14 active, got %d", activeCount)
	}
}

func TestSeedDefaults_ExpenseOrderAndValues(t *testing.T) {
	g := testutil.DB(t)
	uid := uuid.New()
	if err := categories.SeedDefaults(g, uid); err != nil {
		t.Fatalf("SeedDefaults: %v", err)
	}
	var rows []models.Category
	g.Where("user_id = ? AND type = ?", uid, models.TypeExpense).Order("sort_order").Find(&rows)
	wantNames := []string{"Food & Drinks", "Transport", "Shopping", "Bills & Utilities", "Housing", "Health", "Entertainment", "Education", "Other Expense"}
	if len(rows) != len(wantNames) {
		t.Fatalf("want %d expense, got %d", len(wantNames), len(rows))
	}
	for i, name := range wantNames {
		if rows[i].Name != name {
			t.Errorf("row %d: want name %q, got %q", i, name, rows[i].Name)
		}
		if rows[i].SortOrder != i+1 {
			t.Errorf("row %d: want sort_order %d, got %d", i, i+1, rows[i].SortOrder)
		}
	}
	// Check Food & Drinks
	if rows[0].Icon != "utensils" || rows[0].Color != "orange" {
		t.Errorf("Food & Drinks: want utensils/orange, got %s/%s", rows[0].Icon, rows[0].Color)
	}
	// Check Other Expense
	last := rows[len(rows)-1]
	if last.Icon != "circle-dot" || last.Color != "cyan" {
		t.Errorf("Other Expense: want circle-dot/cyan, got %s/%s", last.Icon, last.Color)
	}
}

func TestSeedDefaults_IncomeOrderAndValues(t *testing.T) {
	g := testutil.DB(t)
	uid := uuid.New()
	if err := categories.SeedDefaults(g, uid); err != nil {
		t.Fatalf("SeedDefaults: %v", err)
	}
	var rows []models.Category
	g.Where("user_id = ? AND type = ?", uid, models.TypeIncome).Order("sort_order").Find(&rows)
	wantNames := []string{"Salary", "Bonus", "Freelance", "Gift", "Other Income"}
	if len(rows) != len(wantNames) {
		t.Fatalf("want %d income, got %d", len(wantNames), len(rows))
	}
	for i, name := range wantNames {
		if rows[i].Name != name {
			t.Errorf("row %d: want name %q, got %q", i, name, rows[i].Name)
		}
		if rows[i].SortOrder != i+1 {
			t.Errorf("row %d: want sort_order %d, got %d", i, i+1, rows[i].SortOrder)
		}
	}
	if rows[0].Icon != "briefcase" || rows[0].Color != "green" {
		t.Errorf("Salary: want briefcase/green, got %s/%s", rows[0].Icon, rows[0].Color)
	}
	last := rows[len(rows)-1]
	if last.Icon != "coins" || last.Color != "amber" {
		t.Errorf("Other Income: want coins/amber, got %s/%s", last.Icon, last.Color)
	}
}

func TestSeedDefaults_IconsAndColorsAreValid(t *testing.T) {
	g := testutil.DB(t)
	uid := uuid.New()
	if err := categories.SeedDefaults(g, uid); err != nil {
		t.Fatalf("SeedDefaults: %v", err)
	}
	if len(categories.Colors) != 12 {
		t.Errorf("want 12 colors, got %d", len(categories.Colors))
	}
	if len(categories.Icons) != 24 {
		t.Errorf("want 24 icons, got %d", len(categories.Icons))
	}
	iconSet := make(map[string]bool, len(categories.Icons))
	for _, ic := range categories.Icons {
		iconSet[ic] = true
	}
	colorSet := make(map[string]bool, len(categories.Colors))
	for _, col := range categories.Colors {
		colorSet[col] = true
	}
	var rows []models.Category
	g.Where("user_id = ?", uid).Find(&rows)
	for _, row := range rows {
		if !iconSet[row.Icon] {
			t.Errorf("row %q has unknown icon %q", row.Name, row.Icon)
		}
		if !colorSet[row.Color] {
			t.Errorf("row %q has unknown color %q", row.Name, row.Color)
		}
	}
}

func TestSeedDefaults_OtherUserUnaffected(t *testing.T) {
	g := testutil.DB(t)
	uid1 := uuid.New()
	uid2 := uuid.New()
	if err := categories.SeedDefaults(g, uid1); err != nil {
		t.Fatalf("seed uid1: %v", err)
	}
	if err := categories.SeedDefaults(g, uid2); err != nil {
		t.Fatalf("seed uid2: %v", err)
	}
	var c1, c2 int64
	g.Model(&models.Category{}).Where("user_id = ?", uid1).Count(&c1)
	g.Model(&models.Category{}).Where("user_id = ?", uid2).Count(&c2)
	if c1 != 14 {
		t.Errorf("uid1: want 14, got %d", c1)
	}
	if c2 != 14 {
		t.Errorf("uid2: want 14, got %d", c2)
	}
}

func TestSeedDefaults_SecondCallFails(t *testing.T) {
	g := testutil.DB(t)
	uid := uuid.New()
	if err := categories.SeedDefaults(g, uid); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	err := categories.SeedDefaults(g, uid)
	if !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Errorf("want gorm.ErrDuplicatedKey, got %v", err)
	}
}
