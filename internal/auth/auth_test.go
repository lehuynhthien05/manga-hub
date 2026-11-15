package auth_test

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	"mangahub/internal/auth"
)

const secret = "test-secret-0123456789"

func TestPasswordHashing(t *testing.T) {
	hash, err := auth.HashPassword("Secret123", bcrypt.MinCost)
	require.NoError(t, err)
	assert.True(t, auth.CheckPassword(hash, "Secret123"))
	assert.False(t, auth.CheckPassword(hash, "Wrong1234"))
}

func TestNewManager_EmptySecret(t *testing.T) {
	_, err := auth.NewManager("", time.Hour)
	assert.ErrorIs(t, err, auth.ErrEmptySecret)
}

func TestJWT_RoundTrip(t *testing.T) {
	m, _ := auth.NewManager(secret, 24*time.Hour)
	tok, exp, err := m.Issue("usr_1", "alice", "admin")
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(24*time.Hour), exp, 5*time.Second)

	c, err := m.Verify(tok)
	require.NoError(t, err)
	assert.Equal(t, "usr_1", c.Subject)
	assert.Equal(t, "alice", c.Username)
	assert.Equal(t, "admin", c.Role)
}

func TestJWT_Rejected(t *testing.T) {
	m, _ := auth.NewManager(secret, time.Hour)
	tok, _, _ := m.Issue("usr_1", "alice", "user")

	expired, _ := auth.NewManager(secret, -time.Minute)
	expTok, _, _ := expired.Issue("usr_1", "alice", "user")

	other, _ := auth.NewManager("another-secret-0123456789", time.Hour)
	otherTok, _, _ := other.Issue("usr_1", "alice", "user")

	none, _ := jwt.NewWithClaims(jwt.SigningMethodNone, auth.Claims{
		Role: "admin", RegisteredClaims: jwt.RegisteredClaims{Subject: "usr_1",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)

	// Swap the payload for one claiming admin; the signature no longer matches.
	parts := strings.Split(tok, ".")
	parts[1] = base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"usr_1","role":"admin","exp":9999999999}`))
	tampered := strings.Join(parts, ".")

	for name, bad := range map[string]string{
		"expired": expTok, "wrong secret": otherTok, "alg none": none, "tampered": tampered, "garbage": "x.y.z",
	} {
		_, err := m.Verify(bad)
		assert.ErrorIs(t, err, auth.ErrInvalidToken, name)
	}
}
