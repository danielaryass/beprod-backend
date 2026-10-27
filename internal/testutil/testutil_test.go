package testutil_test

import (
	"testing"
	"time"

	"beprod/internal/jakarta"
	"beprod/internal/models"
	"beprod/internal/testutil"
)

func TestDB_TruncatesBetweenCalls(t *testing.T) {
	g := testutil.DB(t)

	u := models.User{Name: "U", Email: "a@x.com", Role: models.RoleUser, Status: models.UserActive}
	if err := g.Create(&u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	var count int64
	g.Model(&models.User{}).Count(&count)
	if count != 1 {
		t.Fatalf("expected 1 user, got %d", count)
	}

	g = testutil.DB(t)
	g.Model(&models.User{}).Count(&count)
	if count != 0 {
		t.Fatalf("expected 0 users after truncate, got %d", count)
	}
}

func TestSetNow_FreezesAndRestores(t *testing.T) {
	fixed := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)

	t.Run("frozen", func(t *testing.T) {
		testutil.SetNow(t, fixed)
		if !jakarta.Now().Equal(fixed) {
			t.Fatalf("expected frozen time %v, got %v", fixed, jakarta.Now())
		}
	})

	// After t.Run completes, cleanup should have restored jakarta.Now
	since := time.Since(jakarta.Now())
	if since > time.Minute || since < -time.Minute {
		t.Fatalf("jakarta.Now not restored to real time, since = %v", since)
	}
}

func TestSetNow_DrivesCreatedAt(t *testing.T) {
	fixed := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	g := testutil.DB(t)
	testutil.SetNow(t, fixed)

	u := models.User{Name: "U", Email: "b@x.com", Role: models.RoleUser, Status: models.UserActive}
	if err := g.Create(&u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	var loaded models.User
	if err := g.First(&loaded, "id = ?", u.ID).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}

	if !loaded.CreatedAt.Equal(fixed) {
		t.Fatalf("expected CreatedAt %v, got %v", fixed, loaded.CreatedAt)
	}
}

func TestDB_Reusable(t *testing.T) {
	g1 := testutil.DB(t)
	if g1 == nil {
		t.Fatal("first DB() returned nil")
	}
	if err := g1.Exec("SELECT 1").Error; err != nil {
		t.Fatalf("first DB() not working: %v", err)
	}

	g2 := testutil.DB(t)
	if g2 == nil {
		t.Fatal("second DB() returned nil")
	}
	if err := g2.Exec("SELECT 1").Error; err != nil {
		t.Fatalf("second DB() not working: %v", err)
	}
}
