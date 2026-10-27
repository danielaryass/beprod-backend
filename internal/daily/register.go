package daily

import (
	"beprod/internal/app"
	"beprod/internal/session"

	"github.com/gofiber/fiber/v2"
)

func Register(api fiber.Router, d app.Deps) {
	h := api.Group("/habits", session.Require(d))
	h.Get("/", listHabits(d))
	h.Post("/", createHabit(d))
	h.Put("/order", orderHabits(d)) // before /:id
	h.Patch("/:id", updateHabit(d))
	h.Delete("/:id", deleteHabit(d))
}
