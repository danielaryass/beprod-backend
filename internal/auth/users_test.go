package auth_test

import (
	"errors"
	"testing"

	"gorm.io/gorm"

	"beprod/internal/auth"
	"beprod/internal/models"
	"beprod/internal/testutil"

	"github.com/google/uuid"
)

func TestCreateUser_DefaultsRoleAndSeeds(t *testing.T) {
	g := testutil.DB(t)
	var u models.User
	var err error
	txErr := g.Transaction(func(tx *gorm.DB) error {
		u, err = auth.CreateUser(tx, auth.NewUser{Name: "A", Email: "a@x.com", Status: models.UserActive})
		return err
	})
	if txErr != nil {
		t.Fatalf("CreateUser: %v", txErr)
	}
	if u.ID == uuid.Nil {
		t.Error("want non-nil ID")
	}
	if u.Role != "user" {
		t.Errorf("want role 'user', got %q", u.Role)
	}
	if u.Status != "active" {
		t.Errorf("want status 'active', got %q", u.Status)
	}
	var count int64
	g.Model(&models.Category{}).Where("user_id = ?", u.ID).Count(&count)
	if count != 14 {
		t.Errorf("want 14 categories, got %d", count)
	}
}

func TestCreateUser_ExplicitRoleAndFields(t *testing.T) {
	g := testutil.DB(t)
	gs := "google-sub-1"
	by := uuid.New()
	var u models.User
	txErr := g.Transaction(func(tx *gorm.DB) error {
		var err error
		u, err = auth.CreateUser(tx, auth.NewUser{
			Name:          "Admin",
			Email:         "admin@x.com",
			Role:          models.RoleSuperAdmin,
			Status:        models.UserActive,
			GoogleSubject: &gs,
			CreatedBy:     &by,
		})
		return err
	})
	if txErr != nil {
		t.Fatalf("CreateUser: %v", txErr)
	}
	// Re-read from DB
	var got models.User
	g.First(&got, "id = ?", u.ID)
	if got.Role != models.RoleSuperAdmin {
		t.Errorf("want role %q, got %q", models.RoleSuperAdmin, got.Role)
	}
	if got.GoogleSubject == nil || *got.GoogleSubject != gs {
		t.Errorf("want GoogleSubject %q, got %v", gs, got.GoogleSubject)
	}
	if got.CreatedBy == nil || *got.CreatedBy != by {
		t.Errorf("want CreatedBy %v, got %v", by, got.CreatedBy)
	}
}

func TestCreateUser_DuplicateEmail(t *testing.T) {
	g := testutil.DB(t)
	txErr := g.Transaction(func(tx *gorm.DB) error {
		_, err := auth.CreateUser(tx, auth.NewUser{Name: "A", Email: "a@x.com", Status: models.UserActive})
		return err
	})
	if txErr != nil {
		t.Fatalf("first CreateUser: %v", txErr)
	}
	// Second attempt with same email
	txErr2 := g.Transaction(func(tx *gorm.DB) error {
		_, err := auth.CreateUser(tx, auth.NewUser{Name: "B", Email: "a@x.com", Status: models.UserActive})
		return err
	})
	if !errors.Is(txErr2, gorm.ErrDuplicatedKey) {
		t.Errorf("want gorm.ErrDuplicatedKey, got %v", txErr2)
	}
	// Total categories should be 14 (only for the first user)
	var catCount int64
	g.Model(&models.Category{}).Count(&catCount)
	if catCount != 14 {
		t.Errorf("want 14 total categories, got %d", catCount)
	}
	var userCount int64
	g.Model(&models.User{}).Count(&userCount)
	if userCount != 1 {
		t.Errorf("want 1 user, got %d", userCount)
	}
}
