package api

import (
	"reflect"
	"strconv"
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

// structFieldTag looks up a field's struct tag on an anonymous Body struct
// (e.g. CreateFolderInput{}.Body), failing the test rather than panicking if
// the field does not exist — the field names below are load-bearing and a
// rename should surface as a clear test failure, not a runtime panic.
func structFieldTag(t *testing.T, body any, field string) reflect.StructTag {
	t.Helper()
	f, ok := reflect.TypeOf(body).FieldByName(field)
	if !ok {
		t.Fatalf("no field %q on %T", field, body)
	}
	return f.Tag
}

// TestSchemaTagLiteralsMatchTheirConstants guards maxLength:"60" and
// maxItems:"10" struct tags against drifting from maxResourceNameLength and
// maxTagsPerLink. Huma's schema tags are bare literals — a struct tag cannot
// reference a Go constant — so nothing but a test stops one side from
// changing without the other.
func TestSchemaTagLiteralsMatchTheirConstants(t *testing.T) {
	wantMaxLength := strconv.Itoa(maxResourceNameLength)
	for _, tc := range []struct {
		desc string
		tag  reflect.StructTag
	}{
		{"CreateFolderInput.Body.Name", structFieldTag(t, CreateFolderInput{}.Body, "Name")},
		{"UpdateFolderInput.Body.Name", structFieldTag(t, UpdateFolderInput{}.Body, "Name")},
		{"CreateTagInput.Body.Name", structFieldTag(t, CreateTagInput{}.Body, "Name")},
		{"UpdateTagInput.Body.Name", structFieldTag(t, UpdateTagInput{}.Body, "Name")},
	} {
		got, ok := tc.tag.Lookup("maxLength")
		if !ok {
			t.Errorf("%s: no maxLength struct tag", tc.desc)
			continue
		}
		if got != wantMaxLength {
			t.Errorf("%s: maxLength tag is %q, want %q to match maxResourceNameLength",
				tc.desc, got, wantMaxLength)
		}
	}

	wantMaxItems := strconv.Itoa(maxTagsPerLink)
	for _, tc := range []struct {
		desc string
		tag  reflect.StructTag
	}{
		{"CreateLinkInput.Body.TagIDs", structFieldTag(t, CreateLinkInput{}.Body, "TagIDs")},
		{"UpdateLinkInput.Body.TagIDs", structFieldTag(t, UpdateLinkInput{}.Body, "TagIDs")},
	} {
		got, ok := tc.tag.Lookup("maxItems")
		if !ok {
			t.Errorf("%s: no maxItems struct tag", tc.desc)
			continue
		}
		if got != wantMaxItems {
			t.Errorf("%s: maxItems tag is %q, want %q to match maxTagsPerLink",
				tc.desc, got, wantMaxItems)
		}
	}
}
