package auth

import (
	"strings"

	"beprod/internal/app"
	"beprod/internal/httpx"
	"beprod/internal/models"
	"beprod/internal/secure"
	"beprod/internal/session"

	"github.com/gofiber/fiber/v2"
)

func updateMe(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var body struct {
			Name   *string `json:"name"`
			Payday *int    `json:"payday"`
		}
		if err := httpx.Bind(c, &body); err != nil {
			return err
		}
		fields := map[string]string{}
		updates := map[string]any{}
		if body.Name != nil {
			name := strings.TrimSpace(*body.Name)
			if msg := ValidateName(name); msg != "" {
				fields["name"] = msg
			}
			updates["name"] = name
		}
		if body.Payday != nil {
			if *body.Payday < 1 || *body.Payday > 31 {
				fields["payday"] = "Choose a day from 1 to 31"
			}
			updates["payday"] = *body.Payday
		}
		if len(fields) > 0 {
			return httpx.Validation(fields)
		}
		u := session.User(c)
		if len(updates) > 0 {
			if err := d.DB.Model(&models.User{}).Where("id = ?", u.ID).Updates(updates).Error; err != nil {
				return err
			}
		}
		if body.Name != nil {
			u.Name = updates["name"].(string)
		}
		if body.Payday != nil {
			u.Payday = *body.Payday
		}
		return c.JSON(fiber.Map{"user": ToMeDTO(*u)})
	}
}

func changePassword(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var body struct {
			CurrentPassword string `json:"currentPassword"`
			NewPassword     string `json:"newPassword"`
		}
		if err := httpx.Bind(c, &body); err != nil {
			return err
		}
		u := session.User(c)
		if u.PasswordHash == nil {
			return httpx.NewError(400, "NO_PASSWORD", "You signed up with Google. Use 'Forgot password' to set a password.")
		}
		fields := map[string]string{}
		if !secure.CheckPassword(*u.PasswordHash, body.CurrentPassword) {
			fields["currentPassword"] = "Current password is incorrect"
		}
		if msg := ValidatePassword(body.NewPassword); msg != "" {
			fields["newPassword"] = msg
		}
		if len(fields) > 0 {
			return httpx.Validation(fields)
		}
		hash, err := secure.HashPassword(body.NewPassword)
		if err != nil {
			return err
		}
		if err := d.DB.Model(&models.User{}).Where("id = ?", u.ID).Update("password_hash", hash).Error; err != nil {
			return err
		}
		return c.SendStatus(fiber.StatusNoContent)
	}
}
