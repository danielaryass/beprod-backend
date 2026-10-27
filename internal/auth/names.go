package auth

import "strings"

// TruncateRunes cuts s to at most n runes.
func TruncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// NameFromEmail returns the part before "@", cut to 60 runes; "User" if that is empty.
func NameFromEmail(email string) string {
	local := email
	if i := strings.Index(email, "@"); i >= 0 {
		local = email[:i]
	}
	local = strings.TrimSpace(TruncateRunes(local, 60))
	if local == "" {
		return "User"
	}
	return local
}
