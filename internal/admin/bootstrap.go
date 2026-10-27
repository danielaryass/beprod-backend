package admin

import (
	"errors"
	"log"

	"beprod/internal/auth"
	"beprod/internal/config"
	"beprod/internal/models"

	"gorm.io/gorm"
)

// Bootstrap makes cfg.SuperAdminEmail the one super admin. Called from main after db.Migrate.
func Bootstrap(db *gorm.DB, cfg config.Config) error {
	email := auth.NormalizeEmail(cfg.SuperAdminEmail)
	if email == "" {
		log.Printf("SUPER_ADMIN_EMAIL not set; no super admin configured")
		return nil
	}
	hash, err := defaultPasswordHash()
	if err != nil {
		return err
	}
	created := false
	err = db.Transaction(func(tx *gorm.DB) error {
		// demote first: only one super_admin row may exist
		if err := tx.Model(&models.User{}).Where("role = ? AND email <> ?", models.RoleSuperAdmin, email).Update("role", models.RoleUser).Error; err != nil {
			return err
		}
		var u models.User
		err := tx.Where("email = ?", email).First(&u).Error
		switch {
		case err == nil:
			if u.Role != models.RoleSuperAdmin {
				if err := tx.Model(&models.User{}).Where("id = ?", u.ID).Update("role", models.RoleSuperAdmin).Error; err != nil {
					return err
				}
				u.Role = models.RoleSuperAdmin
			}
		case errors.Is(err, gorm.ErrRecordNotFound):
			created = true
			_, err = auth.CreateUser(tx, auth.NewUser{Name: auth.NameFromEmail(email), Email: email, PasswordHash: hash, Role: models.RoleSuperAdmin, Status: models.UserActive})
			return err
		default:
			return err
		}
		// an owner left "invited" by an older version can't accept the emailed link, so give them the default password
		if u.Status == models.UserInvited {
			created = true
			return tx.Model(&models.User{}).Where("id = ?", u.ID).Updates(map[string]any{"password_hash": *hash, "status": models.UserActive}).Error
		}
		return nil
	})
	if err != nil {
		return err
	}
	if created {
		log.Printf("super admin %s can log in with the default password %q; change it in Settings", email, DefaultPassword)
	}
	return nil
}
