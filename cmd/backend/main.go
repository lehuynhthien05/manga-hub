// Command backend is the MangaHub REST + WebSocket + TCP + UDP server.
// Phase 1 stub: it only loads and logs its configuration.
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/joho/godotenv"
	"mangahub/internal/config"
	"mangahub/internal/logging"
)

func main() {
	envErr := godotenv.Load() // a real environment variable wins over .env
	cfg, err := config.LoadBackend(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "backend: invalid configuration:\n"+err.Error())
		os.Exit(2)
	}
	log := logging.New("backend", cfg.LogLevel)
	if envErr != nil && !errors.Is(envErr, fs.ErrNotExist) {
		log.Warn("could not read .env", "err", envErr)
	}
	if cfg.SecretIsPlaceholder() {
		log.Warn("JWT_SECRET still has the .env.example value; change it")
	}
	// The secret and admin password are deliberately not logged.
	log.Info("effective config",
		"http", cfg.HTTPAddr, "tcp", cfg.TCPAddr, "udp", cfg.UDPAddr, "catalog", cfg.CatalogAddr,
		"grpc_timeout", cfg.GRPCTimeout, "db", cfg.DBPath, "jwt_ttl", cfg.JWTTTL,
		"admin_user", cfg.AdminUsername, "redis", cfg.RedisEnabled, "redis_addr", cfg.RedisAddr,
		"cache_ttl", cfg.CacheTTL)
}
