package recurring

import (
	"github.com/gofiber/fiber/v2"

	"beprod/internal/app"
	"beprod/internal/session"
)

func Register(api fiber.Router, d app.Deps) {
	g := api.Group("/recurring", session.Require(d))
	g.Get("/", listHandler(d))
	g.Post("/rules", createRule(d))
	g.Put("/rules/:id", updateRule(d))
	g.Post("/rules/:id/pause", pauseRule(d))
	g.Post("/rules/:id/resume", resumeRule(d))
	g.Delete("/rules/:id", deleteRule(d))
	g.Post("/occurrences/:id/confirm", confirmHandler(d))
	g.Post("/occurrences/:id/skip", skipHandler(d))
}
