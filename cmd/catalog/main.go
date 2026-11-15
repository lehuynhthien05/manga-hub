// Command catalog is the MangaHub gRPC catalog service. It runs as its own
// process; the backend reaches it only over gRPC.
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"mangahub/data"
	"mangahub/internal/catalog"
	"mangahub/internal/config"
	"mangahub/internal/logging"
	"mangahub/pkg/database"
)

func main() {
	envErr := godotenv.Load() // a real environment variable wins over .env
	cfg, err := config.LoadCatalog(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "catalog: invalid configuration:\n"+err.Error())
		os.Exit(2)
	}
	log := logging.New("catalog", cfg.LogLevel)
	if envErr != nil && !errors.Is(envErr, fs.ErrNotExist) {
		log.Warn("could not read .env", "err", envErr)
	}
	if err := run(cfg, log); err != nil {
		log.Error("catalog stopped", "err", err)
		os.Exit(1)
	}
}

func run(cfg config.Catalog, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := database.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := catalog.Migrate(ctx, db); err != nil {
		return err
	}

	seed := data.MangaSeeds
	if cfg.SeedPath != "" {
		if seed, err = os.ReadFile(cfg.SeedPath); err != nil {
			return fmt.Errorf("read SEED_PATH: %w", err)
		}
	}
	store := catalog.NewStore(db)
	seeded, err := store.SeedIfEmpty(ctx, seed) // a failed seed on an empty DB is fatal
	if err != nil {
		return err
	}
	_, count, err := store.Search(ctx, catalog.SearchParams{Limit: 1})
	if err != nil {
		return err
	}

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return err
	}
	srv, hs := catalog.NewGRPCServer(store, log)
	go func() {
		<-ctx.Done()
		hs.Shutdown() // report NOT_SERVING before the port closes
		done := make(chan struct{})
		go func() { srv.GracefulStop(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			srv.Stop()
		}
	}()
	log.Info("catalog ready", "addr", lis.Addr().String(), "db", cfg.DBPath, "manga", count, "seeded", seeded)
	return srv.Serve(lis)
}
