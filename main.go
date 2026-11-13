package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	fmt.Println("=== Launching MangaHub ===")
	fmt.Println("Ports Configuration:")
	fmt.Println("  • Catalog Service (gRPC):         port 9092")
	fmt.Println("  • Backend HTTP REST & WebSocket:  port 8080")
	fmt.Println("  • Backend TCP Progress Sync:      port 9090")
	fmt.Println("  • Backend UDP Chapter Alerts:     port 9091")
	fmt.Println("  • Terminal UI:                    runs in terminal")
	fmt.Println("==========================")

	// 1. Start Catalog Service in background
	catalogCmd := exec.Command("go", "run", "./cmd/catalog")
	catalogCmd.Stdout = os.Stdout
	catalogCmd.Stderr = os.Stderr
	if err := catalogCmd.Start(); err != nil {
		log.Fatalf("Failed to start Catalog: %v", err)
	}

	// 2. Start Backend Service in background
	backendCmd := exec.Command("go", "run", "./cmd/backend")
	backendCmd.Stdout = os.Stdout
	backendCmd.Stderr = os.Stderr
	if err := backendCmd.Start(); err != nil {
		if catalogCmd.Process != nil {
			_ = catalogCmd.Process.Kill()
		}
		log.Fatalf("Failed to start Backend: %v", err)
	}

	// Cleanup child processes on exit
	cleanup := func() {
		fmt.Println("\nStopping background services...")
		if catalogCmd.Process != nil {
			_ = catalogCmd.Process.Signal(syscall.SIGTERM)
		}
		if backendCmd.Process != nil {
			_ = backendCmd.Process.Signal(syscall.SIGTERM)
		}
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		cleanup()
		os.Exit(0)
	}()

	// Brief wait for servers to bind ports
	time.Sleep(1 * time.Second)

	// 3. Start TUI in foreground (interactive terminal)
	tuiCmd := exec.Command("go", "run", "./cmd/tui")
	tuiCmd.Stdin = os.Stdin
	tuiCmd.Stdout = os.Stdout
	tuiCmd.Stderr = os.Stderr

	if err := tuiCmd.Run(); err != nil {
		fmt.Printf("TUI exited: %v\n", err)
	}

	cleanup()
}
