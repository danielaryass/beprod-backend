package auth_test

import (
	"strings"
	"testing"

	"beprod/internal/auth"
)

func TestNameFromEmail(t *testing.T) {
	if got := auth.NameFromEmail("ann@x.com"); got != "ann" {
		t.Fatalf("got %q", got)
	}
	if got := auth.NameFromEmail(strings.Repeat("a", 70) + "@x.com"); len([]rune(got)) != 60 {
		t.Fatalf("got %d runes", len([]rune(got)))
	}
	if got := auth.NameFromEmail("@x.com"); got != "User" {
		t.Fatalf("got %q", got)
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := auth.TruncateRunes("héllo", 3); got != "hél" {
		t.Fatalf("got %q", got)
	}
}
