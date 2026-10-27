package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

const (
	RoleSuperAdmin = "super_admin"
	RoleUser       = "user"

	UserPendingVerification = "pending_verification"
	UserInvited             = "invited"
	UserActive              = "active"
	UserDeactivated         = "deactivated"

	TokenVerifyEmail   = "verify_email"
	TokenResetPassword = "reset_password"
	TokenInvite        = "invite"

	StatusActive   = "active"
	StatusArchived = "archived"
	StatusInactive = "inactive" // budgets only

	AccountCash       = "cash"
	AccountBank       = "bank"
	AccountEwallet    = "ewallet"
	AccountCreditCard = "credit_card"

	TypeIncome     = "income"
	TypeExpense    = "expense"
	TypeTransfer   = "transfer"
	TypeAdjustment = "adjustment" // signed balance correction; not income or expense

	SourceApp       = "app"
	SourceQuickAdd  = "quick_add"
	SourceRecurring = "recurring"

	FreqDaily   = "daily"
	FreqWeekly  = "weekly"
	FreqMonthly = "monthly"
	FreqYearly  = "yearly"

	RulePaused = "paused" // RuleActive = StatusActive
	RuleEnded  = "ended"

	PausedByUser           = "user"
	PausedAccountArchived  = "account_archived"
	PausedCategoryArchived = "category_archived"

	OccPending   = "pending"
	OccConfirmed = "confirmed"
	OccSkipped   = "skipped"

	GoalActive    = "active"
	GoalCompleted = "completed"

	AuditCreateUser    = "create_user"
	AuditResendInvite  = "resend_invite"
	AuditDeactivate    = "deactivate_user"
	AuditReactivate    = "reactivate_user"
	AuditResetPassword = "reset_password"
)

type User struct {
	ID            uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	Name          string    `gorm:"size:60;not null"`
	Email         string    `gorm:"size:254;not null;uniqueIndex"`
	PasswordHash  *string
	GoogleSubject *string    `gorm:"uniqueIndex"`
	Role          string     `gorm:"size:20;not null;default:user"`
	Status        string     `gorm:"size:30;not null"`
	Payday        int        `gorm:"not null;default:25"`
	CreatedBy     *uuid.UUID `gorm:"type:uuid"`
	CreatedAt     time.Time
	LastLoginAt   *time.Time
}

type Session struct {
	ID         uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID     uuid.UUID `gorm:"type:uuid;not null;index"`
	TokenHash  string    `gorm:"size:64;not null;uniqueIndex"`
	LastSeenAt time.Time `gorm:"not null"`
	CreatedAt  time.Time
}

type UserToken struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID    uuid.UUID `gorm:"type:uuid;not null;index"`
	Purpose   string    `gorm:"size:20;not null"`
	TokenHash string    `gorm:"size:64;not null;uniqueIndex"`
	ExpiresAt time.Time `gorm:"not null"`
	UsedAt    *time.Time
	CreatedAt time.Time
}

type LoginFailure struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	Email     string    `gorm:"size:254;not null;index"`
	CreatedAt time.Time `gorm:"index"`
}

type AuditLog struct {
	ID           uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	AdminID      uuid.UUID `gorm:"type:uuid;not null"`
	TargetUserID uuid.UUID `gorm:"type:uuid;not null;index"`
	Action       string    `gorm:"size:30;not null"`
	CreatedAt    time.Time
}

type Account struct {
	ID             uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID         uuid.UUID `gorm:"type:uuid;not null;index"`
	Name           string    `gorm:"size:40;not null"`
	Type           string    `gorm:"size:20;not null"`
	OpeningBalance int64     `gorm:"not null;default:0"`
	OpeningDate    time.Time `gorm:"type:date;not null"`
	SortOrder      int       `gorm:"not null;default:0"`
	Status         string    `gorm:"size:20;not null;default:active"`
	CreditLimit    int64     `gorm:"not null;default:0"`
	StatementDay   int       `gorm:"not null;default:0"`
	DueDay         int       `gorm:"not null;default:0"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Goal struct {
	ID           uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID       uuid.UUID  `gorm:"type:uuid;not null;index"`
	Name         string     `gorm:"size:40;not null"`
	TargetAmount int64      `gorm:"not null"`
	TargetDate   *time.Time `gorm:"type:date"`
	AccountID    uuid.UUID  `gorm:"type:uuid;not null;index"`
	Status       string     `gorm:"size:20;not null;default:active"`
	CompletedAt  *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Category struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID    uuid.UUID `gorm:"type:uuid;not null;index"`
	Name      string    `gorm:"size:30;not null"`
	Type      string    `gorm:"size:10;not null"`
	Icon      string    `gorm:"size:40;not null"`
	Color     string    `gorm:"size:20;not null"`
	SortOrder int       `gorm:"not null;default:0"`
	Status    string    `gorm:"size:20;not null;default:active"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Transaction struct {
	ID                    uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID                uuid.UUID      `gorm:"type:uuid;not null"`
	Type                  string         `gorm:"size:10;not null"`
	Amount                int64          `gorm:"not null"`
	OccurredAt            time.Time      `gorm:"not null"`
	AccountID             uuid.UUID      `gorm:"type:uuid;not null;index"`
	ToAccountID           *uuid.UUID     `gorm:"type:uuid;index"`
	CategoryID            *uuid.UUID     `gorm:"type:uuid;index"`
	Note                  string         `gorm:"size:200;not null;default:''"`
	Tags                  pq.StringArray `gorm:"type:text[];not null;default:'{}'"`
	Source                string         `gorm:"size:20;not null"`
	RecurringOccurrenceID *uuid.UUID     `gorm:"type:uuid"`
	IdempotencyKey        *string        `gorm:"size:64"`
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type Budget struct {
	ID              uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID          uuid.UUID  `gorm:"type:uuid;not null;uniqueIndex:ux_budgets_user_category"`
	CategoryID      uuid.UUID  `gorm:"type:uuid;not null;uniqueIndex:ux_budgets_user_category"`
	RolloverEnabled bool       `gorm:"not null;default:false"`
	RolloverSince   *time.Time `gorm:"type:date"`
	Status          string     `gorm:"size:20;not null;default:active"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type BudgetLimit struct {
	ID             uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	BudgetID       uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:ux_budget_limits_month"`
	EffectiveMonth time.Time `gorm:"type:date;not null;uniqueIndex:ux_budget_limits_month"`
	MonthlyLimit   int64     `gorm:"not null"`
}

type RecurringRule struct {
	ID           uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID       uuid.UUID      `gorm:"type:uuid;not null;index"`
	Type         string         `gorm:"size:10;not null"`
	Amount       int64          `gorm:"not null"`
	AccountID    uuid.UUID      `gorm:"type:uuid;not null;index"`
	ToAccountID  *uuid.UUID     `gorm:"type:uuid"`
	CategoryID   *uuid.UUID     `gorm:"type:uuid;index"`
	Note         string         `gorm:"size:200;not null;default:''"`
	Tags         pq.StringArray `gorm:"type:text[];not null;default:'{}'"`
	Frequency    string         `gorm:"size:10;not null"`
	StartDate    time.Time      `gorm:"type:date;not null"`
	EndDate      *time.Time     `gorm:"type:date"`
	NextDueDate  time.Time      `gorm:"type:date;not null"`
	Status       string         `gorm:"size:20;not null;default:active"`
	PausedReason string         `gorm:"size:30;not null;default:''"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type RecurringOccurrence struct {
	ID            uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	RuleID        uuid.UUID      `gorm:"type:uuid;not null;uniqueIndex:ux_occurrence_rule_date"`
	UserID        uuid.UUID      `gorm:"type:uuid;not null;index"`
	DueDate       time.Time      `gorm:"type:date;not null;uniqueIndex:ux_occurrence_rule_date"`
	Status        string         `gorm:"size:20;not null;default:pending"`
	Type          string         `gorm:"size:10;not null"`
	Amount        int64          `gorm:"not null"`
	AccountID     uuid.UUID      `gorm:"type:uuid;not null"`
	ToAccountID   *uuid.UUID     `gorm:"type:uuid"`
	CategoryID    *uuid.UUID     `gorm:"type:uuid"`
	Note          string         `gorm:"size:200;not null;default:''"`
	Tags          pq.StringArray `gorm:"type:text[];not null;default:'{}'"`
	TransactionID *uuid.UUID     `gorm:"type:uuid"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type QuickAddLink struct {
	ID             uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID         uuid.UUID `gorm:"type:uuid;not null;uniqueIndex"`
	TokenHash      string    `gorm:"size:64;not null;uniqueIndex"`
	PinHash        string    `gorm:"not null"`
	Enabled        bool      `gorm:"not null;default:false"`
	FailedAttempts int       `gorm:"not null;default:0"`
	LockedUntil    *time.Time
	LastAccountID  *uuid.UUID `gorm:"type:uuid"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type QuickAddSession struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	LinkID    uuid.UUID `gorm:"type:uuid;not null;index"`
	TokenHash string    `gorm:"size:64;not null;uniqueIndex"`
	ExpiresAt time.Time `gorm:"not null"`
	CreatedAt time.Time
}

type QuickAddLockEvent struct {
	ID       uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	LinkID   uuid.UUID `gorm:"type:uuid;not null;index"`
	LockedAt time.Time `gorm:"not null"`
}

// All lists every model for AutoMigrate.
func All() []any {
	return []any{&User{}, &Session{}, &UserToken{}, &LoginFailure{}, &AuditLog{},
		&Account{}, &Category{}, &Transaction{}, &Budget{}, &BudgetLimit{},
		&RecurringRule{}, &RecurringOccurrence{}, &QuickAddLink{}, &QuickAddSession{}, &QuickAddLockEvent{}, &Goal{}}
}
