package testutil

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"beprod/internal/app"
	"beprod/internal/auth"
	"beprod/internal/config"
	"beprod/internal/db"
	"beprod/internal/jakarta"
	"beprod/internal/mail"
	"beprod/internal/models"
	"beprod/internal/secure"
	"beprod/internal/server"
	"beprod/internal/session"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const DefaultTestDatabaseURL = "postgres://beprod:beprod@localhost:5433/beprod_test?sslmode=disable"

// Password is the plain password of every user made by CreateUser / NewUser.
const Password = "password1"

var (
	dbOnce sync.Once
	dbConn *gorm.DB
	dbErr  error
)

// DB returns a clean test database: connects once per test binary, migrates, truncates every table.
func DB(t *testing.T) *gorm.DB {
	t.Helper()
	secure.BcryptCost = bcrypt.MinCost
	dbOnce.Do(func() {
		url := os.Getenv("TEST_DATABASE_URL")
		if url == "" {
			url = DefaultTestDatabaseURL
		}
		dbConn, dbErr = db.Connect(url)
		if dbErr == nil {
			dbErr = db.Migrate(dbConn)
		}
	})
	if dbErr != nil {
		t.Fatalf("test db: %v (run `make db-up`)", dbErr)
	}
	var tables []string
	if err := dbConn.Raw(`SELECT tablename FROM pg_tables WHERE schemaname = 'public'`).Scan(&tables).Error; err != nil {
		t.Fatalf("list tables: %v", err)
	}
	if len(tables) > 0 {
		quoted := make([]string, len(tables))
		for i, name := range tables {
			quoted[i] = `"` + name + `"`
		}
		if err := dbConn.Exec("TRUNCATE TABLE " + strings.Join(quoted, ", ") + " RESTART IDENTITY CASCADE").Error; err != nil {
			t.Fatalf("truncate: %v", err)
		}
	}
	t.Cleanup(func() { jakarta.Now = time.Now })
	return dbConn
}

// SetNow freezes jakarta.Now (and therefore GORM's CreatedAt/UpdatedAt) at tm until the test ends.
func SetNow(t *testing.T, tm time.Time) {
	t.Helper()
	jakarta.Now = func() time.Time { return tm }
	t.Cleanup(func() { jakarta.Now = time.Now })
}

// TestApp is a fully wired server on top of the clean test database.
type TestApp struct {
	App          *fiber.App
	DB           *gorm.DB
	Mailer       *mail.FakeMailer
	Config       config.Config
	GoogleTokens map[string]app.GoogleClaims // idToken -> claims; unknown token -> error "invalid token"
}

// NewApp builds a TestApp. opts modify the default config BEFORE the server is built
// (changing a.Config afterwards has no effect on the server).
func NewApp(t *testing.T, opts ...func(*config.Config)) *TestApp {
	t.Helper()
	gdb := DB(t)
	cfg := config.Config{
		Port:                "8080",
		AppBaseURL:          "http://app.test",
		PublicSignupEnabled: false,
		SuperAdminEmail:     "",
		GoogleClientID:      "test-client",
		CookieSecure:        false,
	}
	for _, o := range opts {
		o(&cfg)
	}
	a := &TestApp{DB: gdb, Mailer: &mail.FakeMailer{}, Config: cfg, GoogleTokens: map[string]app.GoogleClaims{}}
	a.App = server.New(app.Deps{
		DB:     gdb,
		Config: cfg,
		Mailer: a.Mailer,
		VerifyGoogle: func(ctx context.Context, idToken string) (app.GoogleClaims, error) {
			cl, ok := a.GoogleTokens[idToken]
			if !ok {
				return app.GoogleClaims{}, errors.New("invalid token")
			}
			return cl, nil
		},
	})
	return a
}

// Response is the result of one in-memory HTTP call.
type Response struct {
	Status int
	Body   map[string]any // nil when the body is not a JSON object
	Raw    []byte
	Header http.Header
}

// Decode unmarshals the raw body into v or fails the test.
func (r Response) Decode(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Raw, v); err != nil {
		t.Fatalf("decode body %q: %v", string(r.Raw), err)
	}
}

// ErrCode returns body.error.code or "".
func (r Response) ErrCode() string {
	e, _ := r.Body["error"].(map[string]any)
	s, _ := e["code"].(string)
	return s
}

// Field returns body.error.fields[name] or "".
func (r Response) Field(name string) string {
	e, _ := r.Body["error"].(map[string]any)
	f, _ := e["fields"].(map[string]any)
	s, _ := f[name].(string)
	return s
}

// Cookie returns the value of the named cookie from the Set-Cookie headers, or "".
func (r Response) Cookie(name string) string {
	for _, c := range (&http.Response{Header: r.Header}).Cookies() {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

// Do calls the app. path is the FULL path, e.g. "/api/v1/me". body nil -> no body, else JSON.
// cookie is a raw Cookie header value such as "beprod_session=abc" ("" = none).
// It always sends X-BeProd-CSRF: 1.
func (a *TestApp) Do(t *testing.T, method, path string, body any, cookie string) Response {
	t.Helper()
	headers := map[string]string{}
	if cookie != "" {
		headers["Cookie"] = cookie
	}
	return a.DoH(t, method, path, body, headers)
}

// DoH is Do with arbitrary headers. A header whose value is "" is REMOVED (use {"X-BeProd-CSRF": ""} to drop the CSRF header).
func (a *TestApp) DoH(t *testing.T, method, path string, body any, headers map[string]string) Response {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-BeProd-CSRF", "1")
	for k, v := range headers {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := a.App.Test(req, -1)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		m = nil
	}
	return Response{Status: resp.StatusCode, Body: m, Raw: raw, Header: resp.Header}
}

// CreateUser makes an active user with password Password (email must already be lower-case).
func (a *TestApp) CreateUser(t *testing.T, email string) models.User {
	t.Helper()
	h, err := secure.HashPassword(Password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return a.CreateUserWith(t, auth.NewUser{Name: "Test User", Email: email, PasswordHash: &h, Status: models.UserActive})
}

// CreateUserWith runs auth.CreateUser in a transaction (so default categories are seeded) and fails the test on error.
func (a *TestApp) CreateUserWith(t *testing.T, nu auth.NewUser) models.User {
	t.Helper()
	var u models.User
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		var e error
		u, e = auth.CreateUser(tx, nu)
		return e
	})
	if err != nil {
		t.Fatalf("create user %s: %v", nu.Email, err)
	}
	return u
}

// LoginAs creates a session for the user and returns the Cookie header value "beprod_session=<token>".
func (a *TestApp) LoginAs(t *testing.T, userID uuid.UUID) string {
	t.Helper()
	tok, err := session.Create(a.DB, userID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return session.CookieName + "=" + tok
}

// NewUser = CreateUser + LoginAs.
func (a *TestApp) NewUser(t *testing.T, email string) (models.User, string) {
	t.Helper()
	u := a.CreateUser(t, email)
	return u, a.LoginAs(t, u.ID)
}

var tokenRe = regexp.MustCompile(`token=([A-Za-z0-9_-]+)`)

// MailToken extracts the token from a link like ".../verify-email?token=XYZ" in the mail body.
func MailToken(t *testing.T, m mail.Message) string {
	t.Helper()
	sub := tokenRe.FindStringSubmatch(m.Body)
	if sub == nil {
		t.Fatalf("no token in mail body: %q", m.Body)
	}
	return sub[1]
}
