package server_test

import (
	"io"
	"net/http/httptest"
	"testing"

	"beprod/internal/app"
	"beprod/internal/server"
)

func TestHealthz_OK(t *testing.T) {
	f := server.New(app.Deps{})
	req := httptest.NewRequest("GET", "/healthz", nil)
	resp, err := f.Test(req, -1)
	if err != nil {
		t.Fatalf("f.Test() error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("io.ReadAll error: %v", err)
	}
	if string(body) != `{"status":"ok"}` {
		t.Errorf("body = %q, want %q", string(body), `{"status":"ok"}`)
	}
	ct := resp.Header.Get("Content-Type")
	if len(ct) < len("application/json") || ct[:len("application/json")] != "application/json" {
		t.Errorf("Content-Type = %q, want prefix %q", ct, "application/json")
	}
}

func TestUnknownRoute_404(t *testing.T) {
	f := server.New(app.Deps{})
	req := httptest.NewRequest("GET", "/does-not-exist", nil)
	resp, err := f.Test(req, -1)
	if err != nil {
		t.Fatalf("f.Test() error: %v", err)
	}
	if resp.StatusCode != 404 {
		t.Errorf("StatusCode = %d, want 404", resp.StatusCode)
	}
}
