package admin

import (
	"time"

	"beprod/internal/models"

	"github.com/google/uuid"
)

// AdminUserDTO is everything the admin may see about a user; never add financial fields (FR-2.6).
type AdminUserDTO struct {
	ID                uuid.UUID  `json:"id"`
	Name              string     `json:"name"`
	Email             string     `json:"email"`
	Role              string     `json:"role"`
	Status            string     `json:"status"`
	CreatedAt         time.Time  `json:"createdAt"`
	LastLoginAt       *time.Time `json:"lastLoginAt"`
	TransactionCount  int64      `json:"transactionCount"`
	LastTransactionAt *time.Time `json:"lastTransactionAt"`
	InviteExpired     bool       `json:"inviteExpired"`
}

func ToAdminUserDTO(u models.User, txCount int64, lastTx *time.Time, inviteExpired bool) AdminUserDTO {
	return AdminUserDTO{
		ID:                u.ID,
		Name:              u.Name,
		Email:             u.Email,
		Role:              u.Role,
		Status:            u.Status,
		CreatedAt:         u.CreatedAt,
		LastLoginAt:       u.LastLoginAt,
		TransactionCount:  txCount,
		LastTransactionAt: lastTx,
		InviteExpired:     inviteExpired,
	}
}
