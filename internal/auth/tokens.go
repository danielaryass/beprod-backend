package auth

import (
	"errors"
	"log"
	"time"

	"beprod/internal/app"
	"beprod/internal/config"
	"beprod/internal/jakarta"
	"beprod/internal/mail"
	"beprod/internal/models"
	"beprod/internal/secure"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var ErrTokenInvalid = errors.New("token invalid") // unknown hash or wrong purpose
var ErrTokenExpired = errors.New("token expired") // expires_at < now OR used_at != nil

// IssueToken marks all unused tokens of this user+purpose as used (used_at = now), creates a new one, returns the plain token.
func IssueToken(tx *gorm.DB, userID uuid.UUID, purpose string, ttl time.Duration) (string, error) {
	now := jakarta.Now()
	if err := tx.Model(&models.UserToken{}).
		Where("user_id = ? AND purpose = ? AND used_at IS NULL", userID, purpose).
		Update("used_at", now).Error; err != nil {
		return "", err
	}
	plain, hash := secure.NewToken()
	if err := tx.Create(&models.UserToken{UserID: userID, Purpose: purpose, TokenHash: hash, ExpiresAt: now.Add(ttl)}).Error; err != nil {
		return "", err
	}
	return plain, nil
}

// FindToken looks the token up by secure.HashToken(plain) AND purpose.
// Not found -> ErrTokenInvalid. Used or expired -> the filled token and ErrTokenExpired.
func FindToken(tx *gorm.DB, plain, purpose string) (models.UserToken, error) {
	var tok models.UserToken
	err := tx.Where("token_hash = ? AND purpose = ?", secure.HashToken(plain), purpose).First(&tok).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.UserToken{}, ErrTokenInvalid
	}
	if err != nil {
		return models.UserToken{}, err
	}
	if tok.UsedAt != nil || tok.ExpiresAt.Before(jakarta.Now()) {
		return tok, ErrTokenExpired
	}
	return tok, nil
}

func MarkUsed(tx *gorm.DB, id uuid.UUID) error {
	return tx.Model(&models.UserToken{}).Where("id = ?", id).Update("used_at", jakarta.Now()).Error
}

// sendMail sends and logs (never returns) the error: callers still succeed when mail fails.
func sendMail(d app.Deps, m mail.Message) {
	if err := d.Mailer.Send(m); err != nil {
		log.Printf("send mail to %s: %v", m.To, err)
	}
}

// tokenLink builds cfg.AppBaseURL + path + "?token=" + plain, e.g. tokenLink(d.Config, "/verify-email", plain).
func tokenLink(cfg config.Config, path, plain string) string {
	return cfg.AppBaseURL + path + "?token=" + plain
}
