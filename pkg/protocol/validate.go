package protocol

import (
	"errors"
	"regexp"
	"unicode"
)

var (
	ErrInvalidUsername = errors.New("username must be 3-32 characters: letters, digits, '_' or '-'")
	ErrWeakPassword    = errors.New("password must be 8-72 characters with upper case, lower case and a digit")
	usernameRe         = regexp.MustCompile(`^[A-Za-z0-9_-]{3,32}$`)
)

func ValidateUsername(u string) error {
	if !usernameRe.MatchString(u) {
		return ErrInvalidUsername
	}
	return nil
}

func ValidatePassword(p string) error {
	if len(p) < 8 || len(p) > 72 { // bcrypt ignores bytes after 72
		return ErrWeakPassword
	}
	var upper, lower, digit bool
	for _, r := range p {
		switch {
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsLower(r):
			lower = true
		case unicode.IsDigit(r):
			digit = true
		}
	}
	if !upper || !lower || !digit {
		return ErrWeakPassword
	}
	return nil
}
