package transactions_test

import (
	"reflect"
	"strings"
	"testing"

	"beprod/internal/transactions"
)

func TestNormalizeTags_OK(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"normalize+dedupe", []string{"#Food", "food", "Trip-2026"}, []string{"food", "trip-2026"}},
		{"nil", nil, []string{}},
		{"empty", []string{}, []string{}},
		{"trim and skip blanks", []string{"  #a  ", "", "#"}, []string{"a"}},
		{"five unique", []string{"a", "b", "c", "d", "e"}, []string{"a", "b", "c", "d", "e"}},
		{"six with dup", []string{"a", "b", "c", "d", "e", "A"}, []string{"a", "b", "c", "d", "e"}},
		{"20 chars", []string{strings.Repeat("a", 20)}, []string{strings.Repeat("a", 20)}},
	}
	for _, c := range cases {
		got, msg := transactions.NormalizeTags(c.in)
		if msg != "" || got == nil || !reflect.DeepEqual([]string(got), c.want) {
			t.Errorf("%s: got %v %q, want %v", c.name, got, msg, c.want)
		}
	}
}

func TestNormalizeTags_Errors(t *testing.T) {
	const chars = "Tags can only use letters, digits and hyphens (max 20 characters)"
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"six unique", []string{"a", "b", "c", "d", "e", "f"}, "You can add up to 5 tags"},
		{"space", []string{"bad tag"}, chars},
		{"underscore", []string{"a_b"}, chars},
		{"accent", []string{"é"}, chars},
		{"double hash", []string{"##a"}, chars},
		{"21 chars", []string{strings.Repeat("a", 21)}, chars},
		{"six with invalid", []string{"a", "b", "c", "d", "e", "bad!"}, chars},
	}
	for _, c := range cases {
		got, msg := transactions.NormalizeTags(c.in)
		if msg != c.want || got == nil || len(got) != 0 {
			t.Errorf("%s: got %v %q, want msg %q", c.name, got, msg, c.want)
		}
	}
}
