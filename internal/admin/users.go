package admin

import (
	"strconv"
	"strings"
	"time"

	"beprod/internal/app"
	"beprod/internal/httpx"
	"beprod/internal/jakarta"
	"beprod/internal/models"
	"beprod/internal/session"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func listUsers(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		const pageSize = 50
		page, err := strconv.Atoi(c.Query("page"))
		if err != nil || page < 1 {
			page = 1
		}
		q := d.DB.Model(&models.User{})
		if search := strings.TrimSpace(c.Query("search")); search != "" {
			escaped := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(search)
			like := "%" + escaped + "%"
			q = q.Where("name ILIKE ? OR email ILIKE ?", like, like)
		}
		var total int64
		if err := q.Session(&gorm.Session{}).Count(&total).Error; err != nil {
			return err
		}
		var users []models.User
		if err := q.Session(&gorm.Session{}).Order("created_at DESC, id").Offset((page - 1) * pageSize).Limit(pageSize).Find(&users).Error; err != nil {
			return err
		}

		type txStat struct {
			UserID uuid.UUID
			Cnt    int64
			LastAt time.Time
		}
		stat := map[uuid.UUID]txStat{}
		hasInvite := map[uuid.UUID]bool{}
		if len(users) > 0 {
			ids := make([]uuid.UUID, len(users))
			for i, u := range users {
				ids[i] = u.ID
			}
			var stats []txStat
			if err := d.DB.Raw(`SELECT user_id, COUNT(*) AS cnt, MAX(occurred_at) AS last_at FROM transactions WHERE user_id IN ? GROUP BY user_id`, ids).Scan(&stats).Error; err != nil {
				return err
			}
			for _, s := range stats {
				stat[s.UserID] = s
			}
			var validInvite []uuid.UUID
			if err := d.DB.Raw(`SELECT DISTINCT user_id FROM user_tokens WHERE user_id IN ? AND purpose = ? AND used_at IS NULL AND expires_at > ?`, ids, models.TokenInvite, jakarta.Now()).Scan(&validInvite).Error; err != nil {
				return err
			}
			for _, id := range validInvite {
				hasInvite[id] = true
			}
		}

		out := make([]AdminUserDTO, 0, len(users))
		for _, u := range users {
			var cnt int64
			var lastTx *time.Time
			if s, ok := stat[u.ID]; ok {
				cnt = s.Cnt
				last := s.LastAt
				lastTx = &last
			}
			inviteExpired := u.Status == models.UserInvited && !hasInvite[u.ID]
			out = append(out, ToAdminUserDTO(u, cnt, lastTx, inviteExpired))
		}
		return c.JSON(fiber.Map{"users": out, "total": total, "page": page, "pageSize": pageSize})
	}
}

func deactivateUser(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		adm := session.User(c)
		if id == adm.ID {
			return httpx.NewError(400, "CANNOT_DEACTIVATE_SELF", "You can't deactivate yourself")
		}
		err = d.DB.Transaction(func(tx *gorm.DB) error {
			var u models.User
			if err := tx.First(&u, "id = ?", id).Error; err != nil {
				return err
			}
			if u.Status == models.UserDeactivated {
				return nil
			}
			if err := tx.Model(&models.User{}).Where("id = ?", u.ID).Update("status", models.UserDeactivated).Error; err != nil {
				return err
			}
			if err := session.RevokeAll(tx, u.ID); err != nil {
				return err
			}
			if err := tx.Exec("DELETE FROM quick_add_sessions WHERE link_id IN (SELECT id FROM quick_add_links WHERE user_id = ?)", u.ID).Error; err != nil {
				return err
			}
			return tx.Create(&models.AuditLog{AdminID: adm.ID, TargetUserID: u.ID, Action: models.AuditDeactivate}).Error
		})
		if err != nil {
			return notFoundOr(err)
		}
		return c.SendStatus(fiber.StatusNoContent)
	}
}

func reactivateUser(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		adm := session.User(c)
		err = d.DB.Transaction(func(tx *gorm.DB) error {
			var u models.User
			if err := tx.First(&u, "id = ?", id).Error; err != nil {
				return err
			}
			if u.Status != models.UserDeactivated {
				return httpx.NewError(400, "NOT_DEACTIVATED", "This user is not deactivated")
			}
			if err := tx.Model(&models.User{}).Where("id = ?", u.ID).Update("status", models.UserActive).Error; err != nil {
				return err
			}
			return tx.Create(&models.AuditLog{AdminID: adm.ID, TargetUserID: u.ID, Action: models.AuditReactivate}).Error
		})
		if err != nil {
			return notFoundOr(err)
		}
		return c.SendStatus(fiber.StatusNoContent)
	}
}

// resetPassword sets the user's password back to DefaultPassword and signs them out everywhere.
// An invited user becomes active (they can now log in); a deactivated user stays deactivated.
func resetPassword(d app.Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id, err := httpx.ParseID(c, "id")
		if err != nil {
			return err
		}
		adm := session.User(c)
		if id == adm.ID {
			return httpx.NewError(400, "CANNOT_RESET_SELF", "Change your own password in Settings")
		}
		hash, err := defaultPasswordHash()
		if err != nil {
			return err
		}
		err = d.DB.Transaction(func(tx *gorm.DB) error {
			var u models.User
			if err := tx.First(&u, "id = ?", id).Error; err != nil {
				return err
			}
			upd := map[string]any{"password_hash": *hash}
			if u.Status == models.UserInvited {
				upd["status"] = models.UserActive
			}
			if err := tx.Model(&models.User{}).Where("id = ?", u.ID).Updates(upd).Error; err != nil {
				return err
			}
			if err := session.RevokeAll(tx, u.ID); err != nil {
				return err
			}
			return tx.Create(&models.AuditLog{AdminID: adm.ID, TargetUserID: u.ID, Action: models.AuditResetPassword}).Error
		})
		if err != nil {
			return notFoundOr(err)
		}
		return c.SendStatus(fiber.StatusNoContent)
	}
}

// notFoundOr maps a missing record to the 404 error and passes anything else through.
func notFoundOr(err error) error {
	if err == gorm.ErrRecordNotFound {
		return httpx.NotFound()
	}
	return err
}
