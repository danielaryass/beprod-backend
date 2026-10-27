package server_test

import (
	"strings"
	"testing"

	"beprod/internal/testutil"
)

const rlMsg = "Too many requests. Slow down a little."

func exhaust(t *testing.T, a *testutil.TestApp, cookie string) {
	t.Helper()
	for i := 1; i <= 120; i++ {
		if r := a.Do(t, "GET", "/api/v1/me", nil, cookie); r.Status != 200 {
			t.Fatalf("request %d: status %d", i, r.Status)
		}
	}
	if r := a.Do(t, "GET", "/api/v1/me", nil, cookie); r.Status != 429 {
		t.Fatalf("request 121: status %d", r.Status)
	}
}

func TestHardening_HSTSHeader(t *testing.T) {
	a := testutil.NewApp(t)
	r := a.DoH(t, "GET", "/healthz", nil, map[string]string{"X-Forwarded-Proto": "https"})
	if r.Status != 200 {
		t.Fatalf("status %d", r.Status)
	}
	if !strings.Contains(r.Header.Get("Strict-Transport-Security"), "max-age=31536000") {
		t.Fatalf("HSTS header = %q", r.Header.Get("Strict-Transport-Security"))
	}
	if r.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing nosniff")
	}
	r = a.Do(t, "GET", "/healthz", nil, "")
	if r.Status != 200 || r.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("plain request: %d nosniff=%q", r.Status, r.Header.Get("X-Content-Type-Options"))
	}
}

func TestRateLimit_121stRequestBlocked(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := a.NewUser(t, "rl1@example.com")
	exhaust(t, a, cookie)
	r := a.Do(t, "GET", "/api/v1/me", nil, cookie)
	if r.ErrCode() != "TOO_MANY_REQUESTS" {
		t.Fatalf("code %q", r.ErrCode())
	}
	if r.Body["error"].(map[string]any)["message"] != rlMsg {
		t.Fatalf("message %v", r.Body["error"])
	}
}

func TestRateLimit_OtherUserUnaffected(t *testing.T) {
	a := testutil.NewApp(t)
	_, ca := a.NewUser(t, "rl2a@example.com")
	_, cb := a.NewUser(t, "rl2b@example.com")
	exhaust(t, a, ca)
	if r := a.Do(t, "GET", "/api/v1/me", nil, cb); r.Status != 200 {
		t.Fatalf("user B status %d", r.Status)
	}
}

func TestRateLimit_UnauthenticatedStill401(t *testing.T) {
	a := testutil.NewApp(t)
	_, ca := a.NewUser(t, "rl3@example.com")
	exhaust(t, a, ca)
	for _, c := range []string{"", "beprod_session=not-a-real-token"} {
		r := a.Do(t, "GET", "/api/v1/me", nil, c)
		if r.Status != 401 || r.ErrCode() != "UNAUTHENTICATED" {
			t.Fatalf("cookie %q: %d %s", c, r.Status, r.ErrCode())
		}
	}
}

func TestRateLimit_SharedAcrossGroups(t *testing.T) {
	a := testutil.NewApp(t)
	_, c := a.NewUser(t, "rl4@example.com")
	for i := 0; i < 60; i++ {
		for _, p := range []string{"/api/v1/me", "/api/v1/accounts"} {
			if r := a.Do(t, "GET", p, nil, c); r.Status != 200 {
				t.Fatalf("%s #%d: %d", p, i, r.Status)
			}
		}
	}
	for _, p := range []string{"/api/v1/accounts", "/api/v1/me"} {
		if r := a.Do(t, "GET", p, nil, c); r.Status != 429 {
			t.Fatalf("%s over quota: %d", p, r.Status)
		}
	}
}

func TestRateLimit_PublicEndpointsNotLimited(t *testing.T) {
	a := testutil.NewApp(t)
	for i := 1; i <= 130; i++ {
		if r := a.Do(t, "GET", "/healthz", nil, ""); r.Status != 200 {
			t.Fatalf("request %d: %d", i, r.Status)
		}
	}
}
