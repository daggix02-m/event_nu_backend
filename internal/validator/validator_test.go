package validator

import (
	"strings"
	"testing"
)

func TestEmailAcceptsValid(t *testing.T) {
	valid := []string{
		"a@b.co",
		"user.name+tag@example-domain.com",
		"x@sub.example.com",
	}
	for _, email := range valid {
		v := New()
		v.Email(email, "email")
		if !v.Valid() {
			t.Fatalf("expected %q to be valid, got errors: %v", email, v.FieldErrors)
		}
	}
}

func TestEmailRejectsInvalid(t *testing.T) {
	invalid := []string{
		"",
		"no-at-sign",
		"a@",
		"@b.co",
		"a b@c.co",
		"a@b",                                    // no TLD
		"a@" + strings.Repeat("x", 300) + ".com", // absurdly long
	}
	for _, email := range invalid {
		v := New()
		v.Email(email, "email")
		if v.Valid() {
			t.Fatalf("expected %q to be invalid", email)
		}
		if _, ok := v.FieldErrors["email"]; !ok {
			t.Fatalf("expected an error on field email for %q", email)
		}
	}
}

func TestEmailRuneLengthLimit(t *testing.T) {
	long := "a@" + strings.Repeat("b", 254) + ".com"
	v := New()
	v.Email(long, "email")
	if v.Valid() {
		t.Fatal("expected overlong email to be invalid")
	}
}

func TestRequired(t *testing.T) {
	for _, tc := range []struct {
		in    string
		valid bool
	}{
		{"x", true},
		{"  seen-it  ", true},
		{"", false},
		{"   ", false},
	} {
		v := New()
		v.Required(tc.in, "name")
		if v.Valid() != tc.valid {
			t.Fatalf("Required(%q): valid = %v, want %v", tc.in, v.Valid(), tc.valid)
		}
		if !tc.valid {
			if v.FieldErrors["name"] != "is required" {
				t.Fatalf("unexpected message: %v", v.FieldErrors)
			}
		}
	}
}

func TestMinMaxCharsCountRunesNotBytes(t *testing.T) {
	v := New()
	v.MinChars("héllo", 5, "title") // 5 runes, 6 bytes (é is 2 bytes)
	if !v.Valid() {
		t.Fatalf("expected 5 runes to satisfy min 5, got: %v", v.FieldErrors)
	}

	v2 := New()
	v2.MaxChars("héllo", 4, "title")
	if v2.Valid() {
		t.Fatal("expected 5 runes to violate max 4")
	}
}

func TestMinMaxMessages(t *testing.T) {
	v := New()
	v.MinChars("ab", 5, "title")
	if v.FieldErrors["title"] != "must be at least 5 characters" {
		t.Fatalf("unexpected message: %v", v.FieldErrors)
	}
	v = New()
	v.MaxChars("abcdef", 3, "title")
	if v.FieldErrors["title"] != "must be at most 3 characters" {
		t.Fatalf("unexpected message: %v", v.FieldErrors)
	}
}

func TestCheckFirstErrorWins(t *testing.T) {
	v := New()
	v.Check(false, "email", "first message")
	v.Check(false, "email", "second message")
	if v.FieldErrors["email"] != "first message" {
		t.Fatalf("expected the first error to win, got %q", v.FieldErrors["email"])
	}
}

func TestAddErrorDoesNotOverwrite(t *testing.T) {
	v := New()
	v.AddError("field", "one")
	v.AddError("field", "two")
	if v.FieldErrors["field"] != "one" {
		t.Fatalf("expected first error retained, got %q", v.FieldErrors["field"])
	}
}

func TestValidReportsNoErrors(t *testing.T) {
	if v := New(); !v.Valid() {
		t.Fatal("fresh validator must be valid")
	}
	v := New()
	v.Check(true, "x", "no error")
	if !v.Valid() {
		t.Fatal("validator with only passing checks must be valid")
	}
}

func TestMatches(t *testing.T) {
	v := New()
	v.Matches("abc", "abc", "confirm")
	if !v.Valid() {
		t.Fatalf("expected match, got: %v", v.FieldErrors)
	}
	v = New()
	v.Matches("abc", "abd", "confirm")
	if v.Valid() || v.FieldErrors["confirm"] != "does not match" {
		t.Fatalf("expected mismatch recorded, got: %v", v.FieldErrors)
	}
}
