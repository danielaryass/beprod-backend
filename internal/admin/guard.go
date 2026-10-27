package admin

import (
	"beprod/internal/httpx"
	"beprod/internal/models"
	"beprod/internal/session"

	"github.com/gofiber/fiber/v2"
)

// RequireSuperAdmin hides the whole admin area from everyone but the super admin (404, not 403).
func RequireSuperAdmin(c *fiber.Ctx) error {
	if session.User(c).Role != models.RoleSuperAdmin {
		return httpx.NotFound()
	}
	return c.Next()
}
