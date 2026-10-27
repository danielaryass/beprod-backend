package quickadd

import (
	"beprod/internal/app"

	"github.com/gofiber/fiber/v2"
)

// Register mendaftarkan semua route Quick Add. TASK-023 akan menambah registerPublic di sini.
func Register(api fiber.Router, d app.Deps) {
	registerSettings(api, d)
	registerPublic(api, d)
}
