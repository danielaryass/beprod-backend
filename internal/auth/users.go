package auth

import (
	"github.com/google/uuid"
	"gorm.io/gorm"

	"beprod/internal/categories"
	"beprod/internal/models"
)

// NewUser holds the fields needed to create a new user.
type NewUser struct {
	Name          string
	Email         string // already normalized
	PasswordHash  *string
	GoogleSubject *string
	Role          string // "" -> models.RoleUser
	Status        string
	CreatedBy     *uuid.UUID
}

// CreateUser inserts the user and seeds default categories, in the given tx. Duplicate email -> gorm.ErrDuplicatedKey.
func CreateUser(tx *gorm.DB, nu NewUser) (models.User, error) {
	role := nu.Role
	if role == "" {
		role = models.RoleUser
	}
	u := models.User{
		Name:          nu.Name,
		Email:         nu.Email,
		PasswordHash:  nu.PasswordHash,
		GoogleSubject: nu.GoogleSubject,
		Role:          role,
		Status:        nu.Status,
		CreatedBy:     nu.CreatedBy,
	}
	if err := tx.Create(&u).Error; err != nil {
		return models.User{}, err
	}
	if err := categories.SeedDefaults(tx, u.ID); err != nil {
		return models.User{}, err
	}
	return u, nil
}
