package auth_test

import (
	"strings"
	"testing"

	"beprod/internal/auth"
)

const (
	msgEmail = "Enter a valid email address"
	msgName  = "Name must be 1–60 characters"
	msgPass  = "Password must be 8–72 characters with at least one letter and one digit"
)

func TestNormalizeEmail(t *testing.T) {
	for in, want := range map[string]string{"  Foo@Bar.COM ": "foo@bar.com", "a@b.com": "a@b.com", "": ""} {
		if got := auth.NormalizeEmail(in); got != want {
			t.Errorf("NormalizeEmail(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateEmail_Valid(t *testing.T) {
	for _, in := range []string{"a@b.co", "user.name+tag@example.com"} {
		if got := auth.ValidateEmail(in); got != "" {
			t.Errorf("ValidateEmail(%q) = %q", in, got)
		}
	}
}

func TestValidateEmail_Invalid(t *testing.T) {
	for _, in := range []string{"", "plain", "@b.com", "a@", "Name <a@b.com>", "a b@c.com", "a@b.com ", strings.Repeat("a", 250) + "@b.co"} {
		if got := auth.ValidateEmail(in); got != msgEmail {
			t.Errorf("ValidateEmail(%q) = %q", in, got)
		}
	}
}

func TestValidateName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", msgName}, {"A", ""}, {strings.Repeat("a", 60), ""}, {strings.Repeat("a", 61), msgName},
		{strings.Repeat("é", 60), ""}, {strings.Repeat("é", 61), msgName},
	}
	for _, c := range cases {
		if got := auth.ValidateName(c.in); got != c.want {
			t.Errorf("ValidateName(%.10q len %d) = %q", c.in, len(c.in), got)
		}
	}
}

func TestValidatePassword_Valid(t *testing.T) {
	for _, in := range []string{"password1", "abcdefg1", strings.Repeat("a", 71) + "1", "pässwörd1"} {
		if got := auth.ValidatePassword(in); got != "" {
			t.Errorf("ValidatePassword(%q) = %q", in, got)
		}
	}
}

func TestValidatePassword_Invalid(t *testing.T) {
	for _, in := range []string{"short1", "password", "12345678", "", strings.Repeat("a", 72) + "1", strings.Repeat("é", 36) + "1"} {
		if got := auth.ValidatePassword(in); got != msgPass {
			t.Errorf("ValidatePassword(%.10q len %d) = %q", in, len(in), got)
		}
	}
}
