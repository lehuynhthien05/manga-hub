// Command catalog is the MangaHub gRPC catalog service.
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
	cfg, err := config.LoadCatalog(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "catalog: invalid configuration:\n"+err.Error())
		os.Exit(2)
	}
	log := logging.New("catalog", cfg.LogLevel)
	if envErr != nil && !errors.Is(envErr, fs.ErrNotExist) {
		log.Warn("could not read .env", "err", envErr)
	}
	log.Info("effective config", "grpc", cfg.GRPCAddr, "db", cfg.DBPath, "seed_path", cfg.SeedPath)
}
