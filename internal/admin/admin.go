package admin

import (
	"errors"
	"strings"

	"beprod/internal/app"
	"beprod/internal/auth"
	"beprod/internal/httpx"
	"beprod/internal/models"
	"beprod/internal/secure"
	"beprod/internal/session"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

func Register(api fiber.Router, d app.Deps) {
	g := api.Group("/admin", session.Require(d), RequireSuperAdmin)
	g.Get("/users", listUsers(d))
	g.Post("/users", createUser(d))
	g.Post("/users/:id/deactivate", deactivateUser(d))
	g.Post("/users/:id/reactivate", reactivateUser(d))
	g.Post("/users/:id/reset-password", resetPassword(d))
}

// DefaultPassword is given to new accounts and to accounts an admin resets.
// ponytail: no email delivery yet, so the admin tells the user in person; send real invite/reset links once a mailer exists.
const DefaultPassword = "password"

func defaultPasswordHash() (*string, error) {
	h, err := secure.HashPassword(DefaultPassword)
	return &h, err
}

func createUser(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var body struct {
			Name  string `json:"name"`
			Email string `json:"email"`
		}
		if err := httpx.Bind(c, &body); err != nil {
			return err
		}
		name := strings.TrimSpace(body.Name)
		email := auth.NormalizeEmail(body.Email)
		fields := map[string]string{}
		if msg := auth.ValidateName(name); msg != "" {
			fields["name"] = msg
		}
		if msg := auth.ValidateEmail(email); msg != "" {
			fields["email"] = msg
		}
		if len(fields) > 0 {
			return httpx.Validation(fields)
		}
		adminUser := session.User(c)
		hash, err := defaultPasswordHash()
		if err != nil {
			return err
		}
		var user models.User
		err = d.DB.Transaction(func(tx *gorm.DB) error {
			var err error
			user, err = auth.CreateUser(tx, auth.NewUser{Name: name, Email: email, PasswordHash: hash, Status: models.UserActive, CreatedBy: &adminUser.ID})
			if err != nil {
				return err
			}
			return tx.Create(&models.AuditLog{AdminID: adminUser.ID, TargetUserID: user.ID, Action: models.AuditCreateUser}).Error
		})
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return httpx.NewError(409, "EMAIL_TAKEN", "A user with this email already exists")
		}
		if err != nil {
			return err
		}
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"user": ToAdminUserDTO(user, 0, nil, false)})
	}
}
