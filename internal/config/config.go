// Package config loads each program's settings from environment variables.
//
// Load* take a getenv function so tests can pass a map lookup and never touch
// the real environment. Reading a .env file (godotenv) happens only in main.
package config

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"
)

// SecretPlaceholder is the JWT_SECRET value shipped in .env.example.
const SecretPlaceholder = "change-me-to-a-long-random-string"

const minSecretLen = 16

type Backend struct {
	HTTPAddr, TCPAddr, UDPAddr, CatalogAddr string
	GRPCTimeout                             time.Duration
	DBPath                                  string
	JWTSecret                               string
	JWTTTL                                  time.Duration
	AdminUsername, AdminPassword            string
	RedisEnabled                            bool
	RedisAddr                               string
	CacheTTL                                time.Duration
	LogLevel                                string
}

type Catalog struct {
	GRPCAddr string
	DBPath   string
	SeedPath string // empty means "use the embedded seed file"
	LogLevel string
}

type TUI struct {
	API, TCP, UDP, WS string
	LogLevel          string
}

// SecretIsPlaceholder reports whether the secret is still the example value.
func (b Backend) SecretIsPlaceholder() bool { return b.JWTSecret == SecretPlaceholder }

func LoadBackend(getenv func(string) string) (Backend, error) {
	var p parser
	p.getenv = getenv
	b := Backend{
		HTTPAddr:      p.addr("HTTP_ADDR", ":8080"),
		TCPAddr:       p.addr("TCP_ADDR", ":9090"),
		UDPAddr:       p.addr("UDP_ADDR", ":9091"),
		CatalogAddr:   p.addr("CATALOG_ADDR", "127.0.0.1:9092"),
		GRPCTimeout:   p.duration("GRPC_TIMEOUT", 2*time.Second),
		DBPath:        p.str("BACKEND_DB_PATH", "var/backend.db"),
		JWTSecret:     p.str("JWT_SECRET", ""),
		JWTTTL:        p.duration("JWT_TTL", 24*time.Hour),
		AdminUsername: p.str("ADMIN_USERNAME", "admin"),
		AdminPassword: p.str("ADMIN_PASSWORD", ""),
		RedisEnabled:  p.boolean("REDIS_ENABLED", false),
		RedisAddr:     p.addr("REDIS_ADDR", "127.0.0.1:6379"),
		CacheTTL:      p.duration("CACHE_TTL", 30*time.Second),
		LogLevel:      p.str("LOG_LEVEL", "info"),
	}
	if len(b.JWTSecret) < minSecretLen {
		p.errs = append(p.errs, fmt.Errorf("JWT_SECRET: required, at least %d characters", minSecretLen))
	}
	return b, p.err()
}

func LoadCatalog(getenv func(string) string) (Catalog, error) {
	p := parser{getenv: getenv}
	c := Catalog{
		GRPCAddr: p.addr("GRPC_ADDR", ":9092"),
		DBPath:   p.str("CATALOG_DB_PATH", "var/catalog.db"),
		SeedPath: p.str("SEED_PATH", ""),
		LogLevel: p.str("LOG_LEVEL", "info"),
	}
	return c, p.err()
}

func LoadTUI(getenv func(string) string) (TUI, error) {
	p := parser{getenv: getenv}
	t := TUI{
		API:      p.str("MANGAHUB_API", "http://127.0.0.1:8080"),
		TCP:      p.addr("MANGAHUB_TCP", "127.0.0.1:9090"),
		UDP:      p.addr("MANGAHUB_UDP", "127.0.0.1:9091"),
		WS:       p.str("MANGAHUB_WS", "ws://127.0.0.1:8080/ws"),
		LogLevel: p.str("LOG_LEVEL", "info"),
	}
	return t, p.err()
}

// parser collects every problem so the user sees all of them at once.
type parser struct {
	getenv func(string) string
	errs   []error
}

func (p *parser) err() error { return errors.Join(p.errs...) }

func (p *parser) str(key, def string) string {
	if v := p.getenv(key); v != "" {
		return v
	}
	return def
}

func (p *parser) addr(key, def string) string {
	v := p.str(key, def)
	if _, port, err := net.SplitHostPort(v); err != nil {
		p.errs = append(p.errs, fmt.Errorf("%s: %w", key, err))
	} else if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		p.errs = append(p.errs, fmt.Errorf("%s: invalid port %q in %q", key, port, v))
	}
	return v
}

func (p *parser) duration(key string, def time.Duration) time.Duration {
	v := p.getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		p.errs = append(p.errs, fmt.Errorf("%s: %q is not a positive duration like 2s or 24h", key, v))
		return def
	}
	return d
}

func (p *parser) boolean(key string, def bool) bool {
	v := p.getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("%s: %q is not a boolean", key, v))
		return def
	}
	return b
}
