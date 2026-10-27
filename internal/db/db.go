package db

import (
	"time"

	"beprod/internal/jakarta"
	"beprod/internal/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func Connect(url string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(url), &gorm.Config{
		TranslateError: true,
		NowFunc:        func() time.Time { return jakarta.Now() },
		Logger:         logger.Default.LogMode(logger.Warn),
	})
}

// Migrate runs AutoMigrate for every model, then the raw index statements in order.
func Migrate(db *gorm.DB) error {
	if err := db.AutoMigrate(models.All()...); err != nil {
		return err
	}
	for _, stmt := range rawIndexes {
		if err := db.Exec(stmt).Error; err != nil {
			return err
		}
	}
	return nil
}

var rawIndexes = []string{
	`CREATE UNIQUE INDEX IF NOT EXISTS ux_accounts_user_name_active ON accounts (user_id, lower(name)) WHERE status = 'active'`,
	`CREATE UNIQUE INDEX IF NOT EXISTS ux_categories_user_type_name_active ON categories (user_id, type, lower(name)) WHERE status = 'active'`,
	`CREATE UNIQUE INDEX IF NOT EXISTS ux_transactions_user_idem ON transactions (user_id, idempotency_key) WHERE idempotency_key IS NOT NULL`,
	`CREATE INDEX IF NOT EXISTS ix_transactions_user_occurred ON transactions (user_id, occurred_at DESC)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS ux_users_single_super_admin ON users (role) WHERE role = 'super_admin'`,
	`CREATE UNIQUE INDEX IF NOT EXISTS ux_goals_active_account ON goals (account_id) WHERE status = 'active'`,
	`CREATE UNIQUE INDEX IF NOT EXISTS ux_habit_logs_habit_date ON habit_logs (habit_id, date)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS ux_note_entries_prompt_date ON note_entries (prompt_id, date)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS ux_habits_user_name_active ON habits (user_id, lower(name)) WHERE NOT hidden`,
	`CREATE UNIQUE INDEX IF NOT EXISTS ux_note_prompts_user_label_active ON note_prompts (user_id, lower(label)) WHERE NOT hidden`,
}
