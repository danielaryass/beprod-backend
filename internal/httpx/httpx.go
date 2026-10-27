package httpx

import (
	"errors"
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// CSRFHeader is the header name used for CSRF protection.
const CSRFHeader = "X-BeProd-CSRF"

// Error represents a structured API error.
type Error struct {
	Status  int               `json:"-"`
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

// Error implements the error interface.
func (e *Error) Error() string { return e.Code }

// NewError creates a new Error with the given status, code, and message.
func NewError(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// NotFound returns a 404 NOT_FOUND error.
func NotFound() *Error {
	return NewError(404, "NOT_FOUND", "Not found")
}

// Validation returns a 400 VALIDATION_ERROR with field-level details.
func Validation(fields map[string]string) *Error {
	return &Error{
		Status:  400,
		Code:    "VALIDATION_ERROR",
		Message: "Please fix the highlighted fields",
		Fields:  fields,
	}
}

// ErrorHandler is a Fiber error handler that formats errors as JSON.
func ErrorHandler(c *fiber.Ctx, err error) error {
	// 1. Custom *Error
	var e *Error
	if errors.As(err, &e) {
		return c.Status(e.Status).JSON(fiber.Map{"error": e})
	}

	// 2. GORM record not found
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return c.Status(404).JSON(fiber.Map{"error": NotFound()})
	}

	// 3. Fiber built-in errors
	var fe *fiber.Error
	if errors.As(err, &fe) {
		var code string
		switch fe.Code {
		case 404:
			code = "NOT_FOUND"
		case 405:
			code = "METHOD_NOT_ALLOWED"
		case 429:
			code = "TOO_MANY_REQUESTS"
		default:
			code = "HTTP_ERROR"
		}
		return c.Status(fe.Code).JSON(fiber.Map{"error": &Error{
			Status:  fe.Code,
			Code:    code,
			Message: fe.Message,
		}})
	}

	// 4. Unknown error — log detail, return generic message
	log.Printf("internal error: %v", err)
	return c.Status(500).JSON(fiber.Map{"error": NewError(500, "INTERNAL", "Something went wrong")})
}

// CSRF returns a Fiber middleware that enforces CSRF protection on mutating methods.
func CSRF() fiber.Handler {
	return func(c *fiber.Ctx) error {
		switch c.Method() {
		case "POST", "PUT", "PATCH", "DELETE":
			if c.Get(CSRFHeader) != "1" {
				return NewError(403, "CSRF_REQUIRED", "Missing CSRF header")
			}
		}
		return c.Next()
	}
}

// Bind parses the request body into out. Returns a 400 error on failure.
func Bind(c *fiber.Ctx, out any) error {
	if err := c.BodyParser(out); err != nil {
		return NewError(400, "INVALID_BODY", "Invalid request body")
	}
	return nil
}

// ParseID parses a UUID from a route parameter. Returns 404 on failure.
func ParseID(c *fiber.Ctx, param string) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Params(param))
	if err != nil {
		return uuid.Nil, NotFound()
	}
	return id, nil
}
