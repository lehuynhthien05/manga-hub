package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mangahub/internal/config"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

const goodSecret = "0123456789abcdef-secret"

func TestLoadBackend_Defaults(t *testing.T) {
	b, err := config.LoadBackend(env(map[string]string{"JWT_SECRET": goodSecret}))
	require.NoError(t, err)
	assert.Equal(t, ":8080", b.HTTPAddr)
	assert.Equal(t, ":9090", b.TCPAddr)
	assert.Equal(t, ":9091", b.UDPAddr)
	assert.Equal(t, "127.0.0.1:9092", b.CatalogAddr)
	assert.Equal(t, 2*time.Second, b.GRPCTimeout)
	assert.Equal(t, 24*time.Hour, b.JWTTTL)
	assert.Equal(t, "admin", b.AdminUsername)
	assert.False(t, b.RedisEnabled)
	assert.Equal(t, 30*time.Second, b.CacheTTL)
	assert.Equal(t, "var/backend.db", b.DBPath)
}

func TestLoadBackend_Invalid(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"missing secret", map[string]string{}, "JWT_SECRET"},
		{"short secret", map[string]string{"JWT_SECRET": "short"}, "JWT_SECRET"},
		{"missing port", map[string]string{"JWT_SECRET": goodSecret, "HTTP_ADDR": "localhost"}, "HTTP_ADDR"},
		{"port out of range", map[string]string{"JWT_SECRET": goodSecret, "TCP_ADDR": ":99999"}, "TCP_ADDR"},
		{"bad duration", map[string]string{"JWT_SECRET": goodSecret, "GRPC_TIMEOUT": "soon"}, "GRPC_TIMEOUT"},
		{"bad bool", map[string]string{"JWT_SECRET": goodSecret, "REDIS_ENABLED": "maybe"}, "REDIS_ENABLED"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := config.LoadBackend(env(tc.env))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestLoadBackend_Overrides(t *testing.T) {
	b, err := config.LoadBackend(env(map[string]string{
		"JWT_SECRET": goodSecret, "HTTP_ADDR": "127.0.0.1:18080", "GRPC_TIMEOUT": "500ms",
		"REDIS_ENABLED": "true", "ADMIN_PASSWORD": "pw",
	}))
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:18080", b.HTTPAddr)
	assert.Equal(t, 500*time.Millisecond, b.GRPCTimeout)
	assert.True(t, b.RedisEnabled)
	assert.Equal(t, "pw", b.AdminPassword)
}

func TestLoadBackend_PlaceholderSecretIsFlagged(t *testing.T) {
	b, err := config.LoadBackend(env(map[string]string{"JWT_SECRET": config.SecretPlaceholder}))
	require.NoError(t, err)
	assert.True(t, b.SecretIsPlaceholder())
}

func TestLoadCatalog(t *testing.T) {
	c, err := config.LoadCatalog(env(nil))
	require.NoError(t, err)
	assert.Equal(t, ":9092", c.GRPCAddr)
	assert.Equal(t, "var/catalog.db", c.DBPath)
	assert.Empty(t, c.SeedPath)

	_, err = config.LoadCatalog(env(map[string]string{"GRPC_ADDR": "9092"}))
	assert.Error(t, err)
}

func TestLoadTUI(t *testing.T) {
	c, err := config.LoadTUI(env(nil))
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:8080", c.API)
	assert.Equal(t, "127.0.0.1:9090", c.TCP)
	assert.Equal(t, "127.0.0.1:9091", c.UDP)
	assert.Equal(t, "ws://127.0.0.1:8080/ws", c.WS)

	_, err = config.LoadTUI(env(map[string]string{"MANGAHUB_TCP": "nope"}))
	assert.Error(t, err)
}
