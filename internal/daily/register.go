package daily

import (
	"beprod/internal/app"
	"beprod/internal/models"
	"beprod/internal/session"

	"github.com/gofiber/fiber/v2"
)

func Register(api fiber.Router, d app.Deps) {
	h := api.Group("/habits", session.Require(d))
	h.Get("/", listHabits(d))
	h.Post("/", createHabit(d))
	h.Put("/order", orderActive(d, func() any { return &models.Habit{} }, "habit")) // before /:id
	h.Patch("/:id", updateHabit(d))
	h.Delete("/:id", deleteHabit(d))

	p := api.Group("/note-prompts", session.Require(d))
	p.Get("/", listPrompts(d))
	p.Post("/", createPrompt(d))
	p.Put("/order", orderActive(d, func() any { return &models.NotePrompt{} }, "prompt")) // before /:id
	p.Patch("/:id", updatePrompt(d))
	p.Delete("/:id", deletePrompt(d))
}
