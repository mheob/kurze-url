package api

import (
	"strings"
	"testing"
)

func TestValidateResourceNameTrims(t *testing.T) {
	got, err := validateResourceName("  Sommerfest 2026\t")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "Sommerfest 2026" {
		t.Fatalf("got %q, want %q", got, "Sommerfest 2026")
	}
}

func TestValidateResourceNameKeepsCaseAndUnicode(t *testing.T) {
	// The stored value is the display value: umlauts, emoji and inner spaces
	// all survive, and nothing is lowercased.
	for _, name := range []string{"Grüße", "Presse & PR", "Sommerfest 🎉", "ÖPNV"} {
		got, err := validateResourceName(name)
		if err != nil {
			t.Fatalf("%q: unexpected error: %v", name, err)
		}
		if got != name {
			t.Fatalf("got %q, want %q unchanged", got, name)
		}
	}
}

func TestValidateResourceNameRejectsEmpty(t *testing.T) {
	for _, name := range []string{"", "   ", "\t\n"} {
		if _, err := validateResourceName(name); err == nil {
			t.Fatalf("%q: want an error, got none", name)
		}
	}
}

func TestValidateResourceNameCountsCharactersNotBytes(t *testing.T) {
	// 60 umlauts are 120 bytes. A byte-length check would reject a name that
	// is exactly at the limit, which is a bug a German-language project would
	// hit on its first real tag.
	atLimit := strings.Repeat("ü", maxResourceNameLength)
	if _, err := validateResourceName(atLimit); err != nil {
		t.Fatalf("a name of exactly %d characters must be accepted: %v", maxResourceNameLength, err)
	}

	overLimit := strings.Repeat("ü", maxResourceNameLength+1)
	if _, err := validateResourceName(overLimit); err == nil {
		t.Fatalf("a name of %d characters must be rejected", maxResourceNameLength+1)
	}
}

func TestValidateResourceNameMeasuresAfterTrimming(t *testing.T) {
	padded := "  " + strings.Repeat("a", maxResourceNameLength) + "  "
	if _, err := validateResourceName(padded); err != nil {
		t.Fatalf("whitespace must not count toward the limit: %v", err)
	}
}
