package secure_test

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"beprod/internal/secure"
)

// fastCost sets bcrypt cost to minimum for faster tests.
func fastCost(t *testing.T) {
	t.Helper()
	old := secure.BcryptCost
	secure.BcryptCost = bcrypt.MinCost
	t.Cleanup(func() { secure.BcryptCost = old })
}

func TestHashPassword_RoundTrip(t *testing.T) {
	fastCost(t)
	hash, err := secure.HashPassword("password1")
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}
	if hash == "password1" {
		t.Error("hash should not equal the original password")
	}
	if !secure.CheckPassword(hash, "password1") {
		t.Error("CheckPassword should return true for correct password")
	}
	if secure.CheckPassword(hash, "password2") {
		t.Error("CheckPassword should return false for wrong password")
	}
}

func TestHashPassword_DifferentSalts(t *testing.T) {
	fastCost(t)
	h1, err1 := secure.HashPassword("same")
	h2, err2 := secure.HashPassword("same")
	if err1 != nil || err2 != nil {
		t.Fatalf("HashPassword errors: %v, %v", err1, err2)
	}
	if h1 == h2 {
		t.Error("two hashes from the same password should differ (different salts)")
	}
	if !secure.CheckPassword(h1, "same") {
		t.Error("h1 should match 'same'")
	}
	if !secure.CheckPassword(h2, "same") {
		t.Error("h2 should match 'same'")
	}
}

func TestHashPassword_TooLong(t *testing.T) {
	fastCost(t)
	long := strings.Repeat("a", 73)
	_, err := secure.HashPassword(long)
	if err == nil {
		t.Error("HashPassword should return error for 73-byte password")
	}
}

func TestCheckPassword_InvalidHash(t *testing.T) {
	// Should return false, not panic
	result := secure.CheckPassword("not-a-hash", "x")
	if result {
		t.Error("CheckPassword with invalid hash should return false")
	}
}

func TestNewToken_Format(t *testing.T) {
	plain, hash := secure.NewToken()
	if len(plain) != 43 {
		t.Errorf("plain token length = %d, want 43", len(plain))
	}
	// Check base64url characters only: [A-Za-z0-9_-]
	for _, ch := range plain {
		if !((ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '-') {
			t.Errorf("plain token contains invalid character: %q", ch)
		}
	}
	if len(hash) != 64 {
		t.Errorf("hash length = %d, want 64", len(hash))
	}
	if hash != secure.HashToken(plain) {
		t.Error("hash should equal HashToken(plain)")
	}
}

func TestNewToken_Unique(t *testing.T) {
	plain1, _ := secure.NewToken()
	plain2, _ := secure.NewToken()
	if plain1 == plain2 {
		t.Error("two tokens should be different")
	}
}

func TestHashToken_KnownVector(t *testing.T) {
	want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	got := secure.HashToken("abc")
	if got != want {
		t.Errorf("HashToken('abc') = %q, want %q", got, want)
	}
}
