// Command tui is the MangaHub terminal client.
// Phase 1 stub: it only loads and prints its configuration.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"

	"github.com/joho/godotenv"
	"mangahub/internal/config"
	"mangahub/internal/logging"
)

func main() {
	envErr := godotenv.Load() // a real environment variable wins over .env
	// Flags override the environment; they go through the same validation.
	api := flag.String("api", "", "backend HTTP base URL (MANGAHUB_API)")
	tcp := flag.String("tcp", "", "backend TCP address (MANGAHUB_TCP)")
	udp := flag.String("udp", "", "backend UDP address (MANGAHUB_UDP)")
	ws := flag.String("ws", "", "backend WebSocket URL (MANGAHUB_WS)")
	flag.Parse()
	override := map[string]string{"MANGAHUB_API": *api, "MANGAHUB_TCP": *tcp, "MANGAHUB_UDP": *udp, "MANGAHUB_WS": *ws}
	getenv := func(k string) string {
		if v := override[k]; v != "" {
			return v
		}
		return os.Getenv(k)
	}
	cfg, err := config.LoadTUI(getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tui: invalid configuration:\n"+err.Error())
		os.Exit(2)
	}

	// Phase 6.1.2 moves this to var/tui-<pid>.log: a TUI must never write to
	// the terminal it is drawing on. The stub exits before any screen exists.
	log := logging.New("tui", cfg.LogLevel)
	if envErr != nil && !errors.Is(envErr, fs.ErrNotExist) {
		log.Warn("could not read .env", "err", envErr)
	}
	log.Info("effective config", "api", cfg.API, "tcp", cfg.TCP, "udp", cfg.UDP, "ws", cfg.WS)
}
