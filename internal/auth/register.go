package auth

import (
	"beprod/internal/app"
	"beprod/internal/session"

	"github.com/gofiber/fiber/v2"
)

// Register mounts all auth routes. Later auth tasks add their routes inside this function.
func Register(api fiber.Router, d app.Deps) {
	api.Get("/config", getConfig(d))
	api.Get("/me", session.Require(d), getMe)
	api.Post("/auth/login", login(d))
	api.Post("/auth/logout", session.Require(d), logout(d))
	api.Post("/auth/signup", signup(d))
	api.Post("/auth/verify-email", verifyEmail(d))
	api.Post("/auth/resend-verification", resendVerification(d))
	api.Post("/auth/forgot-password", forgotPassword(d))
	api.Post("/auth/reset-password", resetPassword(d))
	api.Patch("/me", session.Require(d), updateMe(d))
	api.Post("/me/password", session.Require(d), changePassword(d))
	api.Post("/auth/google", googleLogin(d))
	api.Post("/auth/accept-invite", acceptInvite(d))
}
