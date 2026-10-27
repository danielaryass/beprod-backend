package httpx_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"beprod/internal/httpx"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func newApp() *fiber.App {
	f := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler})
	f.Get("/boom", func(c *fiber.Ctx) error { return errors.New("secret detail") })
	f.Get("/gorm-nf", func(c *fiber.Ctx) error { return fmt.Errorf("wrapped: %w", gorm.ErrRecordNotFound) })
	f.Get("/custom", func(c *fiber.Ctx) error { return httpx.NewError(409, "EMAIL_TAKEN", "taken") })
	f.Get("/validation", func(c *fiber.Ctx) error {
		return httpx.Validation(map[string]string{"name": "Name is required"})
	})
	f.Get("/fiber429", func(c *fiber.Ctx) error { return fiber.NewError(429, "slow down") })
	f.Get("/fiber405", func(c *fiber.Ctx) error { return fiber.NewError(405, "nope") })
	f.Get("/fiber418", func(c *fiber.Ctx) error { return fiber.NewError(418, "teapot") })
	f.Post("/bind", func(c *fiber.Ctx) error {
		var in struct {
			Name string `json:"name"`
		}
		if err := httpx.Bind(c, &in); err != nil {
			return err
		}
		return c.JSON(fiber.Map{"name": in.Name})
	})
	f.Get("/items/:id", func(c *fiber.Ctx) error {
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"id": id.String()})
	})
	return f
}

// do performs a test HTTP request and returns the status and parsed JSON body.
func do(t *testing.T, f *fiber.App, method, path, body string, headers map[string]string) (status int, out map[string]any) {
	t.Helper()
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := f.Test(req, -1)
	if err != nil {
		t.Fatalf("f.Test error: %v", err)
	}
	defer resp.Body.Close()
	status = resp.StatusCode
	raw, _ := io.ReadAll(resp.Body)
	json.Unmarshal(raw, &out)
	return
}

// errObj extracts the "error" map from a response body.
func errObj(out map[string]any) map[string]any {
	if out == nil {
		return nil
	}
	e, _ := out["error"].(map[string]any)
	return e
}

func TestErrorHandler_CustomError(t *testing.T) {
	f := newApp()
	status, out := do(t, f, "GET", "/custom", "", nil)
	if status != 409 {
		t.Errorf("status = %d, want 409", status)
	}
	e := errObj(out)
	if e["code"] != "EMAIL_TAKEN" {
		t.Errorf("code = %v, want EMAIL_TAKEN", e["code"])
	}
	if e["message"] != "taken" {
		t.Errorf("message = %v, want taken", e["message"])
	}
	if _, ok := e["fields"]; ok {
		t.Error("fields key should not be present")
	}
	if _, ok := e["Status"]; ok {
		t.Error("Status key should not be present")
	}
	if _, ok := e["status"]; ok {
		t.Error("status key should not be present")
	}
}

func TestErrorHandler_Validation(t *testing.T) {
	f := newApp()
	status, out := do(t, f, "GET", "/validation", "", nil)
	if status != 400 {
		t.Errorf("status = %d, want 400", status)
	}
	e := errObj(out)
	if e["code"] != "VALIDATION_ERROR" {
		t.Errorf("code = %v, want VALIDATION_ERROR", e["code"])
	}
	if e["message"] != "Please fix the highlighted fields" {
		t.Errorf("message = %v, want 'Please fix the highlighted fields'", e["message"])
	}
	fields, _ := e["fields"].(map[string]any)
	if fields["name"] != "Name is required" {
		t.Errorf("fields.name = %v, want 'Name is required'", fields["name"])
	}
}

func TestErrorHandler_GormNotFound(t *testing.T) {
	f := newApp()
	status, out := do(t, f, "GET", "/gorm-nf", "", nil)
	if status != 404 {
		t.Errorf("status = %d, want 404", status)
	}
	e := errObj(out)
	if e["code"] != "NOT_FOUND" {
		t.Errorf("code = %v, want NOT_FOUND", e["code"])
	}
	if e["message"] != "Not found" {
		t.Errorf("message = %v, want 'Not found'", e["message"])
	}
}

func TestErrorHandler_FiberErrors(t *testing.T) {
	f := newApp()

	status429, out429 := do(t, f, "GET", "/fiber429", "", nil)
	if status429 != 429 {
		t.Errorf("fiber429 status = %d, want 429", status429)
	}
	e429 := errObj(out429)
	if e429["code"] != "TOO_MANY_REQUESTS" {
		t.Errorf("fiber429 code = %v, want TOO_MANY_REQUESTS", e429["code"])
	}
	if e429["message"] != "slow down" {
		t.Errorf("fiber429 message = %v, want 'slow down'", e429["message"])
	}

	status405, out405 := do(t, f, "GET", "/fiber405", "", nil)
	if status405 != 405 {
		t.Errorf("fiber405 status = %d, want 405", status405)
	}
	e405 := errObj(out405)
	if e405["code"] != "METHOD_NOT_ALLOWED" {
		t.Errorf("fiber405 code = %v, want METHOD_NOT_ALLOWED", e405["code"])
	}

	status418, out418 := do(t, f, "GET", "/fiber418", "", nil)
	if status418 != 418 {
		t.Errorf("fiber418 status = %d, want 418", status418)
	}
	e418 := errObj(out418)
	if e418["code"] != "HTTP_ERROR" {
		t.Errorf("fiber418 code = %v, want HTTP_ERROR", e418["code"])
	}
}

func TestErrorHandler_UnknownRoute404(t *testing.T) {
	f := newApp()
	status, out := do(t, f, "GET", "/nope", "", nil)
	if status != 404 {
		t.Errorf("status = %d, want 404", status)
	}
	e := errObj(out)
	if e["code"] != "NOT_FOUND" {
		t.Errorf("code = %v, want NOT_FOUND", e["code"])
	}
}

func TestErrorHandler_UnknownErrorIs500(t *testing.T) {
	f := newApp()
	req := httptest.NewRequest("GET", "/boom", nil)
	resp, err := f.Test(req, -1)
	if err != nil {
		t.Fatalf("f.Test error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 500 {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	json.Unmarshal(raw, &out)
	e := errObj(out)
	if e["code"] != "INTERNAL" {
		t.Errorf("code = %v, want INTERNAL", e["code"])
	}
	if e["message"] != "Something went wrong" {
		t.Errorf("message = %v, want 'Something went wrong'", e["message"])
	}
	if strings.Contains(string(raw), "secret detail") {
		t.Error("response body should not contain 'secret detail'")
	}
}

func TestCSRF_BlocksMutatingWithoutHeader(t *testing.T) {
	f := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler})
	f.Use(httpx.CSRF())
	f.All("/x", func(c *fiber.Ctx) error { return c.SendStatus(200) })

	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		status, out := do(t, f, method, "/x", "", nil)
		if status != 403 {
			t.Errorf("%s without header: status = %d, want 403", method, status)
		}
		e := errObj(out)
		if e["code"] != "CSRF_REQUIRED" {
			t.Errorf("%s without header: code = %v, want CSRF_REQUIRED", method, e["code"])
		}
		if e["message"] != "Missing CSRF header" {
			t.Errorf("%s without header: message = %v, want 'Missing CSRF header'", method, e["message"])
		}
	}
}

func TestCSRF_AllowsWithHeader(t *testing.T) {
	f := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler})
	f.Use(httpx.CSRF())
	f.All("/x", func(c *fiber.Ctx) error { return c.SendStatus(200) })

	headers := map[string]string{httpx.CSRFHeader: "1"}
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		status, _ := do(t, f, method, "/x", "", headers)
		if status != 200 {
			t.Errorf("%s with header: status = %d, want 200", method, status)
		}
	}
}

func TestCSRF_WrongValueRejected(t *testing.T) {
	f := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler})
	f.Use(httpx.CSRF())
	f.All("/x", func(c *fiber.Ctx) error { return c.SendStatus(200) })

	headers0 := map[string]string{httpx.CSRFHeader: "0"}
	status0, out0 := do(t, f, "POST", "/x", "", headers0)
	if status0 != 403 {
		t.Errorf("CSRF=0: status = %d, want 403", status0)
	}
	e0 := errObj(out0)
	if e0["code"] != "CSRF_REQUIRED" {
		t.Errorf("CSRF=0: code = %v, want CSRF_REQUIRED", e0["code"])
	}

	headersEmpty := map[string]string{httpx.CSRFHeader: ""}
	statusE, outE := do(t, f, "POST", "/x", "", headersEmpty)
	if statusE != 403 {
		t.Errorf("CSRF='': status = %d, want 403", statusE)
	}
	eE := errObj(outE)
	if eE["code"] != "CSRF_REQUIRED" {
		t.Errorf("CSRF='': code = %v, want CSRF_REQUIRED", eE["code"])
	}
}

func TestCSRF_SafeMethodsPass(t *testing.T) {
	f := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler})
	f.Use(httpx.CSRF())
	f.All("/x", func(c *fiber.Ctx) error { return c.SendStatus(200) })

	for _, method := range []string{"GET", "HEAD"} {
		status, _ := do(t, f, method, "/x", "", nil)
		if status != 200 {
			t.Errorf("%s without header: status = %d, want 200", method, status)
		}
	}
}

func TestBind_InvalidJSON(t *testing.T) {
	f := newApp()
	status, out := do(t, f, "POST", "/bind", `{"name":`, nil)
	if status != 400 {
		t.Errorf("status = %d, want 400", status)
	}
	e := errObj(out)
	if e["code"] != "INVALID_BODY" {
		t.Errorf("code = %v, want INVALID_BODY", e["code"])
	}
	if e["message"] != "Invalid request body" {
		t.Errorf("message = %v, want 'Invalid request body'", e["message"])
	}
}

func TestBind_NoContentType(t *testing.T) {
	f := newApp()
	req := httptest.NewRequest("POST", "/bind", strings.NewReader(`{"name":"a"}`))
	// explicitly no Content-Type
	resp, err := f.Test(req, -1)
	if err != nil {
		t.Fatalf("f.Test error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	var out map[string]any
	raw, _ := io.ReadAll(resp.Body)
	json.Unmarshal(raw, &out)
	e := errObj(out)
	if e["code"] != "INVALID_BODY" {
		t.Errorf("code = %v, want INVALID_BODY", e["code"])
	}
}

func TestBind_OK(t *testing.T) {
	f := newApp()
	status, out := do(t, f, "POST", "/bind", `{"name":"Budi"}`, nil)
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if out["name"] != "Budi" {
		t.Errorf("name = %v, want 'Budi'", out["name"])
	}
}

func TestParseID_InvalidUUID(t *testing.T) {
	f := newApp()
	status, out := do(t, f, "GET", "/items/not-a-uuid", "", nil)
	if status != 404 {
		t.Errorf("status = %d, want 404", status)
	}
	e := errObj(out)
	if e["code"] != "NOT_FOUND" {
		t.Errorf("code = %v, want NOT_FOUND", e["code"])
	}
}

func TestParseID_Valid(t *testing.T) {
	f := newApp()
	id := uuid.NewString()
	status, out := do(t, f, "GET", "/items/"+id, "", nil)
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if out["id"] != id {
		t.Errorf("id = %v, want %v", out["id"], id)
	}
}

func TestErrorMethod_ReturnsCode(t *testing.T) {
	e := &httpx.Error{Code: "X"}
	if e.Error() != "X" {
		t.Errorf("Error() = %q, want %q", e.Error(), "X")
	}
}
