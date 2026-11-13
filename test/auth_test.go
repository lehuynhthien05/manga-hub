package test

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
	assert.WithinDuration(t, time.Now().Add(24*time.Hour), exp, 5*time.Second)

	claims, err := auth.ParseJWT(token, secret)
	assert.NoError(t, err)
	assert.Equal(t, "usr_123", claims.UserID)
	assert.Equal(t, "johndoe", claims.Username)

	// Wrong secret
	_, err = auth.ParseJWT(token, "wrong-secret")
	assert.Error(t, err)
}
