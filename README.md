# MangaHub

MangaHub tracks manga **metadata and reading progress** (it hosts no manga pages). It is one connected system in which every network protocol does a real job:

| Protocol | Job in MangaHub |
|---|---|
| HTTP (Gin, JSON) | register / login (JWT), search manga, personal library, reading progress |
| TCP | push a progress update to the user's *other* open TUI sessions |
| UDP | best-effort new-chapter alerts to subscribed TUIs |
| WebSocket | live discussion rooms |
| gRPC | backend → separate Catalog Service (search, details, chapter releases) |
| Redis (optional) | cache-aside for manga details |

Three programs, run separately:

| Program | What it is | Listens on |
|---|---|---|
| `cmd/catalog` | gRPC Catalog Service, own SQLite (`var/catalog.db`, table `manga`) | gRPC `:9092` |
| `cmd/backend` | Gin REST + WebSocket + TCP hub + UDP notifier, SQLite (`var/backend.db`: `users`, `user_library`) | HTTP+WS `:8080`, TCP `:9090`, UDP `:9091` |
| `cmd/tui` | terminal UI (tview) — the only user interface | — (client) |

## Status
| Part | State |
|---|---|
| Config, wire types, `.proto` + stubs, schemas, seed data (35 manga) | done |
| Catalog Service (gRPC: `SearchManga`, `GetManga`, `UpdateChapters`, health) | done |
| Backend (accounts, catalog client, library, TCP, UDP, WebSocket) | in progress — currently only loads its config |
| TUI | in progress — currently only loads its config |
| Redis cache | optional, not started |

## Prerequisites
- **Go 1.26 or later** (`go.mod` says `go 1.26.0`; an older Go downloads the toolchain automatically when `GOTOOLCHAIN=auto`).
- **A C compiler** (Xcode Command Line Tools / gcc): the SQLite driver `mattn/go-sqlite3` uses CGO.
- Optional: `protoc` with `protoc-gen-go` and `protoc-gen-go-grpc` (only to regenerate stubs), Redis (only for the cache), `grpcurl` (to poke the catalog by hand).
- The helper script needs bash (macOS/Linux; on Windows use Git Bash or WSL).

## Setup
```bash
cp .env.example .env      # then edit JWT_SECRET (>= 16 chars) and ADMIN_PASSWORD
```
Settings come from environment variables; `.env` is read if present and real environment variables win. Never commit `.env`. Runtime databases are created in `var/` (git-ignored).

## Run
Start each program in its own terminal, from the repository root:
```bash
go run ./cmd/catalog      # terminal 1
go run ./cmd/backend      # terminal 2
go run ./cmd/tui          # terminal 3 (open more TUIs for multi-client checks)
```
Equivalent shortcuts: `make run-catalog`, `make run-backend`, `make run-tui`, or `make build` then `./bin/catalog`, `./bin/backend`, `./bin/tui`. `scripts/run-all.sh` starts catalog and backend in the background and the TUI in the foreground.

On first start the catalog seeds 35 manga from the embedded `data/manga_seeds.json` (only when its table is empty); later starts keep the existing data. Delete `var/*.db*` (`make reset-data`) for a fresh catalog.

## Ports and configuration
| Variable | Default | Used by |
|---|---|---|
| `HTTP_ADDR` / `TCP_ADDR` / `UDP_ADDR` | `:8080` / `:9090` / `:9091` | backend |
| `CATALOG_ADDR`, `GRPC_TIMEOUT` | `127.0.0.1:9092`, `2s` | backend → catalog |
| `GRPC_ADDR` | `:9092` | catalog |
| `BACKEND_DB_PATH`, `CATALOG_DB_PATH` | `var/backend.db`, `var/catalog.db` | backend, catalog |
| `JWT_SECRET` (required), `JWT_TTL` | —, `24h` | backend |
| `ADMIN_USERNAME`, `ADMIN_PASSWORD` | `admin`, — | backend (demo admin) |
| `REDIS_ENABLED`, `REDIS_ADDR`, `CACHE_TTL` | `false`, `127.0.0.1:6379`, `30s` | backend (optional cache) |
| `MANGAHUB_API` / `_TCP` / `_UDP` / `_WS` | `http://127.0.0.1:8080`, `127.0.0.1:9090`, `127.0.0.1:9091`, `ws://127.0.0.1:8080/ws` | TUI (flags `--api --tcp --udp --ws` override) |
| `LOG_LEVEL` | `info` | all |

The full list with comments is in `.env.example`.

## Tests
```bash
go test ./...          # all tests
go test -race ./...    # with the race detector (needs CGO)
```
Tests create their own temporary databases and loopback listeners; they do not need the programs running.

## Regenerating the gRPC stubs
```bash
make proto             # protoc -I proto ... proto/manga.proto → proto/mangaproto/*.pb.go
```
The generated files are committed; regenerate only after changing `proto/manga.proto`.

## Demo accounts
The backend seeds one admin from `ADMIN_USERNAME` / `ADMIN_PASSWORD` (used to trigger chapter releases). Regular demo users (for example `alice`, `bob`, `charlie`) are registered through the TUI. *(Available once the backend is implemented.)*

## Known limitations
- No TLS: tokens travel in clear text on localhost.
- UDP alerts are best-effort: a datagram can be lost; the server counts *sent*, not *delivered*.
- SQLite allows one writer at a time (WAL mode and a busy timeout keep this invisible at demo scale).
- Backend and TUI are still being built (see Status).
