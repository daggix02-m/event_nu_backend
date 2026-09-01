package validator

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var emailPattern = regexp.MustCompile(`^[a-zA-Z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)+$`)

type Validator struct {
	FieldErrors map[string]string
}

func New() *Validator {
	return &Validator{FieldErrors: make(map[string]string)}
}

func (v *Validator) Valid() bool {
	return len(v.FieldErrors) == 0
}

func (v *Validator) AddError(field, message string) {
	if _, exists := v.FieldErrors[field]; !exists {
		v.FieldErrors[field] = message
	}
}

func (v *Validator) Check(ok bool, field, message string) {
	if !ok {
		v.AddError(field, message)
	}
}

func (v *Validator) Email(value, field string) {
	v.Check(value != "" && utf8.RuneCountInString(value) <= 254, field, "must be a valid email address")
	if value != "" {
		v.Check(emailPattern.MatchString(value), field, "must be a valid email address")
	}
}

func (v *Validator) MinChars(value string, n int, field string) {
	v.Check(utf8.RuneCountInString(value) >= n, field, "must be at least "+itoa(n)+" characters")
}

func (v *Validator) MaxChars(value string, n int, field string) {
	v.Check(utf8.RuneCountInString(value) <= n, field, "must be at most "+itoa(n)+" characters")
}

func (v *Validator) Required(value, field string) {
	v.Check(strings.TrimSpace(value) != "", field, "is required")
}

func (v *Validator) Matches(value, other, field string) {
	v.Check(value == other, field, "does not match")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}