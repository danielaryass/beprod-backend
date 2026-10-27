package db_test

import (
	"errors"
	"testing"
	"time"

	"beprod/internal/db"
	"beprod/internal/models"
	"beprod/internal/testutil"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func mkUser(t *testing.T, g *gorm.DB, email, role string) models.User {
	t.Helper()
	u := models.User{Name: "U", Email: email, Role: role, Status: models.UserActive}
	if err := g.Create(&u).Error; err != nil {
		t.Fatalf("create user %s: %v", email, err)
	}
	return u
}

func mkAccount(g *gorm.DB, userID uuid.UUID, name, status string) error {
	return g.Create(&models.Account{UserID: userID, Name: name, Type: models.AccountBank, OpeningDate: day(2026, 10, 1), Status: status}).Error
}

func isDup(err error) bool { return errors.Is(err, gorm.ErrDuplicatedKey) }

func TestMigrate_Idempotent(t *testing.T) {
	g := testutil.DB(t)
	if err := db.Migrate(g); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if err := db.Migrate(g); err != nil {
		t.Fatalf("third migrate: %v", err)
	}
}

func TestMigrate_CreatesAllTables(t *testing.T) {
	g := testutil.DB(t)
	var tables []string
	if err := g.Raw(`SELECT tablename FROM pg_tables WHERE schemaname='public'`).Scan(&tables).Error; err != nil {
		t.Fatalf("query tables: %v", err)
	}
	want := []string{
		"users", "sessions", "user_tokens", "login_failures", "audit_logs",
		"accounts", "categories", "transactions", "budgets", "budget_limits",
		"recurring_rules", "recurring_occurrences", "quick_add_links", "quick_add_sessions", "quick_add_lock_events",
	}
	tableSet := make(map[string]bool, len(tables))
	for _, tbl := range tables {
		tableSet[tbl] = true
	}
	for _, w := range want {
		if !tableSet[w] {
			t.Errorf("missing table: %s", w)
		}
	}
}

func TestAccounts_ActiveNameUniqueCaseInsensitive(t *testing.T) {
	g := testutil.DB(t)
	userA := mkUser(t, g, "a@test.com", models.RoleUser)

	if err := mkAccount(g, userA.ID, "BCA", models.StatusActive); err != nil {
		t.Fatalf("first BCA: %v", err)
	}
	if err := mkAccount(g, userA.ID, "bca", models.StatusActive); !isDup(err) {
		t.Fatalf("expected dup for bca active, got: %v", err)
	}
	if err := mkAccount(g, userA.ID, "Cash", models.StatusActive); err != nil {
		t.Fatalf("different name Cash: %v", err)
	}
}

func TestAccounts_SameNameAllowedForDifferentUsers(t *testing.T) {
	g := testutil.DB(t)
	userA := mkUser(t, g, "a2@test.com", models.RoleUser)
	userB := mkUser(t, g, "b2@test.com", models.RoleUser)

	if err := mkAccount(g, userA.ID, "BCA", models.StatusActive); err != nil {
		t.Fatalf("userA BCA: %v", err)
	}
	if err := mkAccount(g, userB.ID, "BCA", models.StatusActive); err != nil {
		t.Fatalf("userB BCA: %v", err)
	}
}

func TestAccounts_ArchivedDuplicateAllowed(t *testing.T) {
	g := testutil.DB(t)
	userA := mkUser(t, g, "a3@test.com", models.RoleUser)

	// archived BCA
	if err := mkAccount(g, userA.ID, "BCA", models.StatusArchived); err != nil {
		t.Fatalf("BCA archived: %v", err)
	}
	// active BCA ok (partial index only covers active)
	if err := mkAccount(g, userA.ID, "BCA", models.StatusActive); err != nil {
		t.Fatalf("BCA active after archived: %v", err)
	}
	// second archived bca ok (two archived with same name allowed)
	if err := mkAccount(g, userA.ID, "bca", models.StatusArchived); err != nil {
		t.Fatalf("bca archived second: %v", err)
	}
	// second active BCA → dup
	if err := mkAccount(g, userA.ID, "BCA", models.StatusActive); !isDup(err) {
		t.Fatalf("expected dup for second active BCA, got: %v", err)
	}
}

func TestCategories_UniquePerTypeAmongActive(t *testing.T) {
	g := testutil.DB(t)
	userA := mkUser(t, g, "cat@test.com", models.RoleUser)

	mkCat := func(name, typ, status string) error {
		return g.Create(&models.Category{
			UserID: userA.ID, Name: name, Type: typ,
			Icon: "tag", Color: "red", Status: status,
		}).Error
	}

	if err := mkCat("Food", models.TypeExpense, models.StatusActive); err != nil {
		t.Fatalf("Food expense active: %v", err)
	}
	if err := mkCat("food", models.TypeExpense, models.StatusActive); !isDup(err) {
		t.Fatalf("expected dup for food expense active, got: %v", err)
	}
	if err := mkCat("Food", models.TypeIncome, models.StatusActive); err != nil {
		t.Fatalf("Food income active (different type): %v", err)
	}
	if err := mkCat("Food", models.TypeExpense, models.StatusArchived); err != nil {
		t.Fatalf("Food expense archived: %v", err)
	}
}

func TestUsers_SingleSuperAdmin(t *testing.T) {
	g := testutil.DB(t)

	mkUser(t, g, "admin1@test.com", models.RoleSuperAdmin)

	u2 := models.User{Name: "U", Email: "admin2@test.com", Role: models.RoleSuperAdmin, Status: models.UserActive}
	if err := g.Create(&u2).Error; !isDup(err) {
		t.Fatalf("expected dup for second super_admin, got: %v", err)
	}

	mkUser(t, g, "user1@test.com", models.RoleUser)
	mkUser(t, g, "user2@test.com", models.RoleUser)
}

func TestUsers_EmailUnique(t *testing.T) {
	g := testutil.DB(t)

	mkUser(t, g, "dup@test.com", models.RoleUser)

	u2 := models.User{Name: "U", Email: "dup@test.com", Role: models.RoleUser, Status: models.UserActive}
	if err := g.Create(&u2).Error; !isDup(err) {
		t.Fatalf("expected dup for same email, got: %v", err)
	}
}

func TestOccurrences_RuleDateUnique(t *testing.T) {
	g := testutil.DB(t)

	ruleID := uuid.New()
	userID := uuid.New()

	occ1 := models.RecurringOccurrence{
		RuleID:    ruleID,
		UserID:    userID,
		DueDate:   day(2026, 11, 1),
		Status:    models.OccPending,
		Type:      models.TypeExpense,
		Amount:    1000,
		AccountID: uuid.New(),
		Note:      "",
		Tags:      pq.StringArray{},
	}
	if err := g.Create(&occ1).Error; err != nil {
		t.Fatalf("first occurrence: %v", err)
	}

	occ2 := models.RecurringOccurrence{
		RuleID:    ruleID,
		UserID:    userID,
		DueDate:   day(2026, 11, 1),
		Status:    models.OccPending,
		Type:      models.TypeExpense,
		Amount:    1000,
		AccountID: uuid.New(),
		Note:      "",
		Tags:      pq.StringArray{},
	}
	if err := g.Create(&occ2).Error; !isDup(err) {
		t.Fatalf("expected dup for same rule+date, got: %v", err)
	}

	// different date ok
	occ3 := models.RecurringOccurrence{
		RuleID:    ruleID,
		UserID:    userID,
		DueDate:   day(2026, 12, 1),
		Status:    models.OccPending,
		Type:      models.TypeExpense,
		Amount:    1000,
		AccountID: uuid.New(),
		Note:      "",
		Tags:      pq.StringArray{},
	}
	if err := g.Create(&occ3).Error; err != nil {
		t.Fatalf("different date: %v", err)
	}

	// different rule with same date ok
	occ4 := models.RecurringOccurrence{
		RuleID:    uuid.New(),
		UserID:    userID,
		DueDate:   day(2026, 11, 1),
		Status:    models.OccPending,
		Type:      models.TypeExpense,
		Amount:    1000,
		AccountID: uuid.New(),
		Note:      "",
		Tags:      pq.StringArray{},
	}
	if err := g.Create(&occ4).Error; err != nil {
		t.Fatalf("different rule same date: %v", err)
	}
}

func TestBudgets_OnePerUserCategory(t *testing.T) {
	g := testutil.DB(t)

	userID, catID := uuid.New(), uuid.New()

	b1 := models.Budget{UserID: userID, CategoryID: catID, Status: models.StatusActive}
	if err := g.Create(&b1).Error; err != nil {
		t.Fatalf("first budget: %v", err)
	}

	b2 := models.Budget{UserID: userID, CategoryID: catID, Status: models.StatusActive}
	if err := g.Create(&b2).Error; !isDup(err) {
		t.Fatalf("expected dup for same user+category, got: %v", err)
	}

	// different category ok
	b3 := models.Budget{UserID: userID, CategoryID: uuid.New(), Status: models.StatusActive}
	if err := g.Create(&b3).Error; err != nil {
		t.Fatalf("different category: %v", err)
	}

	// different user same category ok
	b4 := models.Budget{UserID: uuid.New(), CategoryID: catID, Status: models.StatusActive}
	if err := g.Create(&b4).Error; err != nil {
		t.Fatalf("different user same category: %v", err)
	}
}

func TestBudgetLimits_OnePerBudgetMonth(t *testing.T) {
	g := testutil.DB(t)

	budgetID := uuid.New()

	bl1 := models.BudgetLimit{BudgetID: budgetID, EffectiveMonth: day(2026, 10, 1), MonthlyLimit: 1000}
	if err := g.Create(&bl1).Error; err != nil {
		t.Fatalf("first budget limit: %v", err)
	}

	bl2 := models.BudgetLimit{BudgetID: budgetID, EffectiveMonth: day(2026, 10, 1), MonthlyLimit: 2000}
	if err := g.Create(&bl2).Error; !isDup(err) {
		t.Fatalf("expected dup for same budget+month, got: %v", err)
	}

	// different month ok
	bl3 := models.BudgetLimit{BudgetID: budgetID, EffectiveMonth: day(2026, 11, 1), MonthlyLimit: 1500}
	if err := g.Create(&bl3).Error; err != nil {
		t.Fatalf("different month: %v", err)
	}
}

func TestTransactions_IdempotencyKeyUniquePerUser(t *testing.T) {
	g := testutil.DB(t)

	userA := uuid.New()
	userB := uuid.New()

	mkTx := func(userID uuid.UUID, key *string) error {
		return g.Create(&models.Transaction{
			UserID:         userID,
			Type:           models.TypeExpense,
			Amount:         1000,
			OccurredAt:     time.Date(2026, 10, 20, 3, 0, 0, 0, time.UTC),
			AccountID:      uuid.New(),
			Source:         models.SourceApp,
			Tags:           pq.StringArray{},
			IdempotencyKey: key,
		}).Error
	}

	k1 := "k1"
	k2 := "k2"

	// user A key k1 ok
	if err := mkTx(userA, &k1); err != nil {
		t.Fatalf("userA k1: %v", err)
	}
	// user A key k1 again → dup
	if err := mkTx(userA, &k1); !isDup(err) {
		t.Fatalf("expected dup for userA k1 again, got: %v", err)
	}
	// user B key k1 ok (different user)
	if err := mkTx(userB, &k1); err != nil {
		t.Fatalf("userB k1: %v", err)
	}
	// user A two nil keys ok
	if err := mkTx(userA, nil); err != nil {
		t.Fatalf("userA nil key 1: %v", err)
	}
	if err := mkTx(userA, nil); err != nil {
		t.Fatalf("userA nil key 2: %v", err)
	}
	// user A key k2 ok
	if err := mkTx(userA, &k2); err != nil {
		t.Fatalf("userA k2: %v", err)
	}
}

func TestIndexes_Exist(t *testing.T) {
	g := testutil.DB(t)

	var indexes []string
	if err := g.Raw(`SELECT indexname FROM pg_indexes WHERE schemaname='public'`).Scan(&indexes).Error; err != nil {
		t.Fatalf("query indexes: %v", err)
	}

	want := []string{
		"ux_accounts_user_name_active",
		"ux_categories_user_type_name_active",
		"ux_transactions_user_idem",
		"ix_transactions_user_occurred",
		"ux_users_single_super_admin",
		"ux_budgets_user_category",
		"ux_budget_limits_month",
		"ux_occurrence_rule_date",
	}

	indexSet := make(map[string]bool, len(indexes))
	for _, idx := range indexes {
		indexSet[idx] = true
	}
	for _, w := range want {
		if !indexSet[w] {
			t.Errorf("missing index: %s", w)
		}
	}
}
