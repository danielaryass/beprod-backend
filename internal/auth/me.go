package auth

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"beprod/internal/app"
	"beprod/internal/models"
	"beprod/internal/session"
)

// MeDTO is the data transfer object for the current user.
type MeDTO struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Email       string    `json:"email"`
	Role        string    `json:"role"`
	Status      string    `json:"status"`
	HasPassword bool      `json:"hasPassword"`
	Payday      int       `json:"payday"`
	CreatedAt   time.Time `json:"createdAt"`
}

// ToMeDTO converts a User model to a MeDTO.
func ToMeDTO(u models.User) MeDTO {
	return MeDTO{
		ID:          u.ID,
		Name:        u.Name,
		Email:       u.Email,
		Role:        u.Role,
		Status:      u.Status,
		HasPassword: u.PasswordHash != nil && *u.PasswordHash != "",
		Payday:      u.Payday,
		CreatedAt:   u.CreatedAt,
	}
}

func getConfig(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"publicSignupEnabled": d.Config.PublicSignupEnabled})
	}
}

func getMe(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"user": ToMeDTO(*session.User(c))})
}
