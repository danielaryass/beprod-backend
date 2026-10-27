package auth_test

import (
	"errors"
	"testing"
	"time"

	"beprod/internal/auth"
	"beprod/internal/models"
	"beprod/internal/testutil"
)

func TestIssueToken_FindToken_OK(t *testing.T) {
	a := testutil.NewApp(t)
	u := a.CreateUser(t, "tok@example.com")
	plain, err := auth.IssueToken(a.DB, u.ID, models.TokenVerifyEmail, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := auth.FindToken(a.DB, plain, models.TokenVerifyEmail)
	if err != nil || tok.UserID != u.ID {
		t.Fatalf("err=%v user=%v", err, tok.UserID)
	}
}

func TestFindToken_UnknownIsInvalid(t *testing.T) {
	a := testutil.NewApp(t)
	if _, err := auth.FindToken(a.DB, "nope", models.TokenVerifyEmail); !errors.Is(err, auth.ErrTokenInvalid) {
		t.Fatalf("got %v", err)
	}
}

func TestFindToken_WrongPurposeIsInvalid(t *testing.T) {
	a := testutil.NewApp(t)
	u := a.CreateUser(t, "tok@example.com")
	plain, _ := auth.IssueToken(a.DB, u.ID, models.TokenVerifyEmail, time.Hour)
	if _, err := auth.FindToken(a.DB, plain, models.TokenResetPassword); !errors.Is(err, auth.ErrTokenInvalid) {
		t.Fatalf("got %v", err)
	}
}

func TestFindToken_ExpiredAfterTTL(t *testing.T) {
	a := testutil.NewApp(t)
	u := a.CreateUser(t, "tok@example.com")
	base := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	testutil.SetNow(t, base)
	plain, _ := auth.IssueToken(a.DB, u.ID, models.TokenVerifyEmail, time.Hour)
	testutil.SetNow(t, base.Add(61*time.Minute))
	tok, err := auth.FindToken(a.DB, plain, models.TokenVerifyEmail)
	if !errors.Is(err, auth.ErrTokenExpired) || tok.UserID != u.ID {
		t.Fatalf("err=%v user=%v", err, tok.UserID)
	}
}

func TestMarkUsed_MakesTokenExpired(t *testing.T) {
	a := testutil.NewApp(t)
	u := a.CreateUser(t, "tok@example.com")
	plain, _ := auth.IssueToken(a.DB, u.ID, models.TokenVerifyEmail, time.Hour)
	tok, _ := auth.FindToken(a.DB, plain, models.TokenVerifyEmail)
	if err := auth.MarkUsed(a.DB, tok.ID); err != nil {
		t.Fatal(err)
	}
	tok, err := auth.FindToken(a.DB, plain, models.TokenVerifyEmail)
	if !errors.Is(err, auth.ErrTokenExpired) || tok.UsedAt == nil {
		t.Fatalf("err=%v usedAt=%v", err, tok.UsedAt)
	}
}

func TestIssueToken_InvalidatesPrevious(t *testing.T) {
	a := testutil.NewApp(t)
	u := a.CreateUser(t, "tok@example.com")
	first, _ := auth.IssueToken(a.DB, u.ID, models.TokenVerifyEmail, time.Hour)
	other, _ := auth.IssueToken(a.DB, u.ID, models.TokenResetPassword, time.Hour)
	second, _ := auth.IssueToken(a.DB, u.ID, models.TokenVerifyEmail, time.Hour)
	if _, err := auth.FindToken(a.DB, first, models.TokenVerifyEmail); !errors.Is(err, auth.ErrTokenExpired) {
		t.Fatalf("first: %v", err)
	}
	if _, err := auth.FindToken(a.DB, second, models.TokenVerifyEmail); err != nil {
		t.Fatalf("second: %v", err)
	}
	if _, err := auth.FindToken(a.DB, other, models.TokenResetPassword); err != nil {
		t.Fatalf("other purpose: %v", err)
	}
}
