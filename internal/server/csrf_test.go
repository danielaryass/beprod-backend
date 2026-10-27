package server_test

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"

	"beprod/internal/app"
	"beprod/internal/server"
)

func call(t *testing.T, method, path, header string) (int, map[string]any) {
	t.Helper()
	f := server.New(app.Deps{})
	req := httptest.NewRequest(method, path, nil)
	if header != "" {
		req.Header.Set("X-BeProd-CSRF", header)
	}
	resp, err := f.Test(req, -1)
	if err != nil {
		t.Fatalf("f.Test error: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func TestCSRF_PostWithoutHeaderIs403(t *testing.T) {
	status, out := call(t, "POST", "/api/v1/anything", "")
	if status != 403 {
		t.Errorf("status = %d, want 403", status)
	}
	e, _ := out["error"].(map[string]any)
	if e["code"] != "CSRF_REQUIRED" {
		t.Errorf("code = %v, want CSRF_REQUIRED", e["code"])
	}
}

func TestCSRF_PutPatchDeleteWithoutHeaderIs403(t *testing.T) {
	for _, method := range []string{"PUT", "PATCH", "DELETE"} {
		status, out := call(t, method, "/api/v1/anything", "")
		if status != 403 {
			t.Errorf("%s: status = %d, want 403", method, status)
		}
		e, _ := out["error"].(map[string]any)
		if e["code"] != "CSRF_REQUIRED" {
			t.Errorf("%s: code = %v, want CSRF_REQUIRED", method, e["code"])
		}
	}
}

func TestCSRF_PostWithHeaderPassesToRouting(t *testing.T) {
	status, out := call(t, "POST", "/api/v1/anything", "1")
	if status != 404 {
		t.Errorf("status = %d, want 404", status)
	}
	e, _ := out["error"].(map[string]any)
	if e["code"] != "NOT_FOUND" {
		t.Errorf("code = %v, want NOT_FOUND", e["code"])
	}
}

func TestCSRF_WrongHeaderValueIs403(t *testing.T) {
	status, out := call(t, "POST", "/api/v1/anything", "0")
	if status != 403 {
		t.Errorf("status = %d, want 403", status)
	}
	e, _ := out["error"].(map[string]any)
	if e["code"] != "CSRF_REQUIRED" {
		t.Errorf("code = %v, want CSRF_REQUIRED", e["code"])
	}
}

func TestCSRF_GetNeedsNoHeader(t *testing.T) {
	status, out := call(t, "GET", "/api/v1/anything", "")
	if status != 404 {
		t.Errorf("status = %d, want 404", status)
	}
	e, _ := out["error"].(map[string]any)
	if e["code"] != "NOT_FOUND" {
		t.Errorf("code = %v, want NOT_FOUND", e["code"])
	}
}

func TestHealthz_OutsideAPIGroupNeedsNoCSRF(t *testing.T) {
	status, _ := call(t, "GET", "/healthz", "")
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
}
