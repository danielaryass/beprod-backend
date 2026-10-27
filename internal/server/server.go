package server

import (
	"beprod/internal/accounts"
	"beprod/internal/admin"
	"beprod/internal/app"
	"beprod/internal/auth"
	"beprod/internal/budgets"
	"beprod/internal/categories"
	"beprod/internal/dashboard"
	"beprod/internal/goals"
	"beprod/internal/httpx"
	"beprod/internal/plan"
	"beprod/internal/quickadd"
	"beprod/internal/recurring"
	"beprod/internal/transactions"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/helmet"
)

func New(d app.Deps) *fiber.App {
	f := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler})
	f.Use(helmet.New(helmet.Config{HSTSMaxAge: 31536000}))
	f.Get("/healthz", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"status": "ok"}) })
	api := f.Group("/api/v1", httpx.CSRF())
	auth.Register(api, d)
	admin.Register(api, d)
	accounts.Register(api, d)
	categories.Register(api, d)
	transactions.Register(api, d)
	budgets.Register(api, d)
	recurring.Register(api, d)
	dashboard.Register(api, d)
	quickadd.Register(api, d)
	goals.Register(api, d)
	plan.Register(api, d)
	return f
}
