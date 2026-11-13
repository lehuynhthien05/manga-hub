#!/usr/bin/env bash
set -e

# MangaHub launcher: runs catalog and backend in background, then opens TUI in foreground.
# When TUI exits or script is cancelled (Ctrl+C), background processes are terminated.

cleanup() {
  echo ""
  echo "Shutting down servers..."
  [ -n "$CATALOG_PID" ] && kill "$CATALOG_PID" 2>/dev/null || true
  [ -n "$BACKEND_PID" ] && kill "$BACKEND_PID" 2>/dev/null || true
}

trap cleanup EXIT INT TERM

# Start Catalog Service (gRPC)
echo "Starting Catalog Service..."
go run ./cmd/catalog &
CATALOG_PID=$!

# Start Backend Service (REST + WS + TCP + UDP)
echo "Starting Backend Service..."
go run ./cmd/backend &
BACKEND_PID=$!

# Wait brief moment for servers to bind ports
sleep 1.5

# Start TUI in foreground (interactive terminal)
echo "Starting TUI..."
go run ./cmd/tui

