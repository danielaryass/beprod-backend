package auth

import (
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"
)

func NormalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// ValidateEmail returns "" when valid. Callers normalize first.
func ValidateEmail(email string) string {
	if len(email) <= 254 {
		if addr, err := mail.ParseAddress(email); err == nil && addr.Address == email {
			return ""
		}
	}
	return "Enter a valid email address"
}

// ValidateName expects an already-trimmed name.
func ValidateName(name string) string {
	if n := utf8.RuneCountInString(name); n < 1 || n > 60 {
		return "Name must be 1–60 characters"
	}
	return ""
}

func ValidatePassword(p string) string {
	letter, digit := false, false
	for _, r := range p {
		letter = letter || unicode.IsLetter(r)
		digit = digit || unicode.IsDigit(r)
	}
	if len(p) < 8 || len(p) > 72 || !letter || !digit {
		return "Password must be 8–72 characters with at least one letter and one digit"
	}
	return ""
}
