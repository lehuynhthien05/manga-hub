package auth_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"mangahub/internal/auth"
)

func TestPasswordHashing(t *testing.T) {
	password := "Secret123!"
	hash, err := auth.HashPassword(password)
	assert.NoError(t, err)
	assert.NotEmpty(t, hash)

	assert.True(t, auth.CheckPasswordHash(password, hash))
	assert.False(t, auth.CheckPasswordHash("WrongPassword1", hash))
}

func TestValidatePassword(t *testing.T) {
	assert.Error(t, auth.ValidatePassword("short"))
	assert.Error(t, auth.ValidatePassword("alllowercase123"))
	assert.Error(t, auth.ValidatePassword("ALLUPPERCASE123"))
	assert.Error(t, auth.ValidatePassword("NoDigitsPassword"))
	assert.NoError(t, auth.ValidatePassword("ValidPass123"))
}

func TestValidateEmail(t *testing.T) {
	assert.Error(t, auth.ValidateEmail("invalid-email"))
	assert.NoError(t, auth.ValidateEmail("user@example.com"))
}

func TestJWTGenerationAndParsing(t *testing.T) {
	secret := "test-secret"
	token, exp, err := auth.GenerateJWT("usr_123", "johndoe", secret)
	assert.NoError(t, err)
	assert.NotEmpty(t, token)
	assert.True(t, exp.After(time.Now()))

	claims, err := auth.ParseJWT(token, secret)
	assert.NoError(t, err)
	assert.Equal(t, "usr_123", claims.UserID)
	assert.Equal(t, "johndoe", claims.Username)

	// Wrong secret
	_, err = auth.ParseJWT(token, "wrong-secret")
	assert.Error(t, err)
}

func TestJWT_EmptySecretRejected(t *testing.T) {
	_, _, err := auth.GenerateJWT("usr_1", "bob", "")
	assert.ErrorIs(t, err, auth.ErrEmptySecret)
	_, err = auth.ParseJWT("x.y.z", "")
	assert.ErrorIs(t, err, auth.ErrEmptySecret)
}
