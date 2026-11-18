// Command tui is the MangaHub terminal client.
// It logs to var/tui-<pid>.log only: writing to stdout/stderr would corrupt the screen.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"runtime/debug"

	"github.com/joho/godotenv"
	"mangahub/internal/config"
	"mangahub/internal/logging"
	"mangahub/internal/tui/app"
	"mangahub/internal/tui/client"
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

	if err := os.MkdirAll("var", 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "tui:", err)
		os.Exit(1)
	}
	logPath := fmt.Sprintf("var/tui-%d.log", os.Getpid())
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tui: open log:", err)
		os.Exit(1)
	}
	defer f.Close()
	log := logging.NewWriter(f, "tui", cfg.LogLevel)
	if envErr != nil && !errors.Is(envErr, fs.ErrNotExist) {
		log.Warn("could not read .env", "err", envErr)
	}
	log.Info("effective config", "api", cfg.API, "tcp", cfg.TCP, "udp", cfg.UDP, "ws", cfg.WS)

	// tview restores the terminal before re-raising a panic from its event loop;
	// we then record the stack in the log and print it on the restored terminal.
	defer func() {
		if p := recover(); p != nil {
			log.Error("panic", "panic", p, "stack", string(debug.Stack()))
			fmt.Fprintf(os.Stderr, "tui crashed: %v (details in %s)\n", p, logPath)
			os.Exit(1)
		}
	}()
	if err := app.New(client.NewAPI(cfg.API), cfg.TCP, log).Run(); err != nil {
		log.Error("tui stopped", "err", err)
		fmt.Fprintln(os.Stderr, "tui:", err)
		os.Exit(1)
	}
	log.Info("tui exited")
}
