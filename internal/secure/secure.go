package secure

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"

	"golang.org/x/crypto/bcrypt"
)

// BcryptCost is the cost factor for bcrypt hashing. Tests can lower it to bcrypt.MinCost.
var BcryptCost = 12

// HashPassword hashes the given password using bcrypt.
func HashPassword(p string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(p), BcryptCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// CheckPassword checks if the given password matches the hash.
func CheckPassword(hash, p string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(p)) == nil
}

// NewToken generates a new random token. Returns the plain token (43 chars base64url)
// and its SHA-256 hex hash (64 chars).
func NewToken() (plain, hash string) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	plain = base64.RawURLEncoding.EncodeToString(b)
	hash = HashToken(plain)
	return
}

// HashToken returns the SHA-256 hex hash of the plain token (64 chars).
func HashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}
