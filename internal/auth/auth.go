// Package auth hashes passwords and issues/verifies HS256 JWTs. The Gin
// middleware here protects HTTP routes; TCP, UDP and WebSocket use Verify.
package auth

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v4"
	"golang.org/x/crypto/bcrypt"
	"mangahub/pkg/protocol"
)

var (
	ErrEmptySecret  = errors.New("jwt secret must not be empty")
	ErrInvalidToken = errors.New("invalid or expired token")
)

// Claims carry the user id in the standard "sub" claim.
type Claims struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// HashPassword uses bcrypt; production passes bcrypt.DefaultCost, tests bcrypt.MinCost.
func HashPassword(password string, cost int) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	return string(b), err
}

func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// Manager issues and verifies tokens with one secret. It implements Verifier.
type Manager struct {
	secret []byte
	ttl    time.Duration
}

func NewManager(secret string, ttl time.Duration) (*Manager, error) {
	if secret == "" {
		return nil, ErrEmptySecret
	}
	return &Manager{secret: []byte(secret), ttl: ttl}, nil
}

func (m *Manager) Issue(userID, username, role string) (string, time.Time, error) {
	now := time.Now()
	exp := now.Add(m.ttl)
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		Username: username,
		Role:     role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
	}).SignedString(m.secret)
	return tok, exp, err
}

// Verify accepts only HS256 (rejects alg=none and algorithm swaps) and checks exp.
func (m *Manager) Verify(token string) (Claims, error) {
	var c Claims
	t, err := jwt.ParseWithClaims(token, &c, func(*jwt.Token) (any, error) { return m.secret, nil },
		jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !t.Valid || c.Subject == "" {
		return Claims{}, ErrInvalidToken
	}
	return c, nil
}

// Context keys set by Middleware.
const (
	keyUserID   = "user_id"
	keyUsername = "username"
	keyRole     = "role"
)

// Middleware requires "Authorization: Bearer <jwt>"; failures get a 401 envelope.
// Parser details go to the debug log, never to the client.
func Middleware(v Verifier, log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		tok, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
		if !ok || tok == "" {
			abort(c, http.StatusUnauthorized, protocol.CodeUnauthenticated, "missing bearer token")
			return
		}
		claims, err := v.Verify(tok)
		if err != nil {
			log.Debug("token rejected", "err", err, "path", c.FullPath())
			abort(c, http.StatusUnauthorized, protocol.CodeUnauthenticated, "invalid or expired token")
			return
		}
		c.Set(keyUserID, claims.Subject)
		c.Set(keyUsername, claims.Username)
		c.Set(keyRole, claims.Role)
		c.Next()
	}
}

// RequireAdmin must run after Middleware.
func RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString(keyRole) != protocol.RoleAdmin {
			abort(c, http.StatusForbidden, protocol.CodeForbidden, "admin role required")
			return
		}
		c.Next()
	}
}

// UserID is the verified caller; handlers must never take a user id from the request.
func UserID(c *gin.Context) string   { return c.GetString(keyUserID) }
func Username(c *gin.Context) string { return c.GetString(keyUsername) }

func abort(c *gin.Context, status int, code, msg string) {
	c.AbortWithStatusJSON(status, protocol.ErrorResponse{Error: msg, Code: code})
}
