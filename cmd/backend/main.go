// Command backend is the MangaHub REST + WebSocket + TCP + UDP server.
// Phases 3-4: HTTP (accounts, JWT, health, manga search via the gRPC catalog).
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"golang.org/x/crypto/bcrypt"
	"mangahub/internal/auth"
	"mangahub/internal/backend/api"
	"mangahub/internal/backend/catalogclient"
	"mangahub/internal/backend/store"
	"mangahub/internal/config"
	"mangahub/internal/logging"
	"mangahub/pkg/database"
	"mangahub/pkg/protocol"
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
	if err := run(cfg, log); err != nil {
		log.Error("backend stopped", "err", err)
		os.Exit(1)
	}
}

func run(cfg config.Backend, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := database.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := store.Migrate(ctx, db); err != nil {
		return err
	}
	st := store.New(db)
	if err := ensureAdmin(ctx, st, cfg, log); err != nil {
		return err
	}
	mgr, err := auth.NewManager(cfg.JWTSecret, cfg.JWTTTL)
	if err != nil {
		return err
	}

	cat, err := catalogclient.New(cfg.CatalogAddr, cfg.GRPCTimeout)
	if err != nil {
		return err
	}
	defer cat.Close() // runs after srv.Shutdown returns, so no request still uses it

	if cfg.LogLevel != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           api.NewRouter(api.Deps{Store: st, Auth: mgr, Catalog: cat, BcryptCost: bcrypt.DefaultCost, Log: log}),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second, // no global Read/WriteTimeout: WebSocket arrives in Phase 9
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("backend ready", "http", cfg.HTTPAddr, "db", cfg.DBPath, "catalog", cfg.CatalogAddr)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(sctx)
}

// ensureAdmin creates the demo admin once; an existing account is never overwritten.
func ensureAdmin(ctx context.Context, st *store.Store, cfg config.Backend, log *slog.Logger) error {
	if cfg.AdminPassword == "" {
		log.Warn("ADMIN_PASSWORD is empty; no admin account is seeded")
		return nil
	}
	_, err := st.GetByUsername(ctx, cfg.AdminUsername)
	if err == nil {
		log.Info("admin user ready", "username", cfg.AdminUsername, "created", false)
		return nil
	}
	if !errors.Is(err, store.ErrUserNotFound) {
		return err
	}
	hash, err := auth.HashPassword(cfg.AdminPassword, bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if _, err := st.CreateUser(ctx, cfg.AdminUsername, hash, protocol.RoleAdmin); err != nil {
		return fmt.Errorf("seed admin: %w", err)
	}
	log.Info("admin user ready", "username", cfg.AdminUsername, "created", true)
	return nil
}
