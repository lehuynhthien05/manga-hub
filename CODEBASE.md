# MangaHub: codebase map

- Commit: `6515802` (branch `feat/library-progress-rest`)
- Written: 2026-11-16
- Working tree also holds uncommitted Docker/deploy files (`Dockerfile`, `compose.yaml`, `deploy.sh`, `.github/workflows/deploy.yml`, `.dockerignore`).

## 1. What it is
A university Net-Centric Programming project (IU, VNU-HCM) that tracks manga **metadata and reading progress** (it hosts no manga pages). One system where each network protocol has a real job: HTTP/JSON, gRPC, TCP, UDP, WebSocket, optional Redis. Three separate programs: a gRPC catalog service, a backend (REST + WS + TCP + UDP), and a terminal UI.

## 2. Stack
- Go 1.26 (`go.mod`: `go 1.26.0`), module name `mangahub`. CGO needed (SQLite driver).
- HTTP: Gin v1.12. Auth: `golang-jwt/jwt/v4`, bcrypt (`x/crypto`). gRPC + protobuf for backend → catalog.
- DB: SQLite via `mattn/go-sqlite3` (two files, WAL mode). Config: env vars, `.env` read by `godotenv`.
- Tests: `testify`. Redis cache is planned (Phase 12), not present.

## 3. Commands (from `Makefile`)
- Run: `go run ./cmd/catalog`, `go run ./cmd/backend`, `go run ./cmd/tui` (or `make run-catalog|run-backend|run-tui`; `scripts/run-all.sh` starts all three)
- Build: `make build` (-> `bin/`)
- Test: `make test` (`go test ./...`), `make race`, `make cover`
- Lint: `make fmt`, `make vet`
- Regenerate gRPC stubs: `make proto` (needs `protoc`)
- Reset local data: `make reset-data` (deletes `var/*.db*`; ask before using)
- Setup: `cp .env.example .env`, set `JWT_SECRET` (>= 16 chars) and `ADMIN_PASSWORD`

## 4. Folder map
| Path | Purpose |
|---|---|
| `cmd/catalog` | catalog service `main` (migrate, seed, gRPC listen) |
| `cmd/backend` | backend `main` (config, DB, admin seed, Gin server) |
| `cmd/tui` | TUI `main`; stub that only loads config so far |
| `main.go` (root) | launcher: runs catalog + backend with `go run`, then the TUI |
| `internal/config` | `LoadBackend/LoadCatalog/LoadTUI` take a `getenv` func; collect all errors at once |
| `internal/auth` | JWT `Manager`, bcrypt helpers, Gin `Middleware`, `RequireAdmin`, `Verifier` interface |
| `internal/backend/api` | Gin router, handlers, and the `seams.go` interfaces |
| `internal/backend/store` | backend SQLite store: `users`, `user_library` (+ `schema.sql`) |
| `internal/backend/catalogclient` | gRPC client implementing `api.Catalog` |
| `internal/catalog` | catalog store (`manga` table, seed, search), gRPC `Server`, `schema.sql` |
| `internal/backend/{synctcp,notifyudp,chatws,cache}` | `doc.go` only: TCP sync, UDP alerts, WebSocket chat, Redis cache not written yet |
| `internal/tui/{app,client}` | `doc.go` only |
| `internal/logging`, `internal/clock`, `internal/testutil` | slog factory, `Clock` + `Fake`, test helpers |
| `internal/integration` | cross-component test (`search_test.go`) |
| `pkg/protocol` | wire types and constants shared by all programs (HTTP, TCP, UDP, WS envelopes, validators) |
| `pkg/database` | `Open` (SQLite DSN options); `db.go` is an older `InitDB` helper (see section 8) |
| `pkg/models` | older model structs (see section 8) |
| `proto/` | `manga.proto` and generated `mangaproto/*.pb.go` (generated: not read) |
| `data/` | `manga_seeds.json` (35 manga) embedded via `data/embed.go` |
| `test/` | `database_test.go` (tests the older `InitDB`) |
| `docs/`, `.agent/` | phase plans and logs; git-ignored, not read |

## 5. Entry points
- `cmd/catalog/main.go` listens on gRPC `:9092`
- `cmd/backend/main.go` listens on HTTP `:8080`; TCP `:9090` and UDP `:9091` are configured but not served yet
- `cmd/tui/main.go` client stub (flags `--api --tcp --udp --ws`)
- `main.go` and `scripts/run-all.sh` launch everything together

## 6. Main flows
Routes (`internal/backend/api/router.go:NewRouter`): `GET /api/health`, `POST /api/auth/register`, `POST /api/auth/login`, `GET /api/manga`, `GET /api/manga/:id`, and under the JWT group `/api/library`: `POST ""`, `GET ""`, `PUT /:mangaId/progress`. Chapter release (`ReleaseRequest` type, `RequireAdmin`) has no route yet.

**Update reading progress** (`PUT /api/library/:mangaId/progress`)
1. `internal/backend/api/router.go:NewRouter` -> `internal/auth/auth.go:Middleware` (Bearer JWT -> `Verifier.Verify`, sets user id in context)
2. `internal/backend/api/library_handlers.go:updateProgress`: validate body (400) -> `internal/backend/store/library.go:GetLibrary` (404 if not owned)
3. If a chapter is sent: `api.Catalog.Get` -> `internal/backend/catalogclient/client.go:Get` -> gRPC `MangaService.GetManga` -> `internal/catalog/server.go:GetManga` -> `internal/catalog/store.go:Get`; the chapter may not exceed `TotalChapters` (400)
4. `internal/backend/store/library.go:UpdateProgress`: one `UPDATE ... RETURNING` that also bumps `version`
5. `ProgressPublisher.Publish(...)` after commit (no-op until Phase 7), then the JSON response

**Add to library** (`POST /api/library`): `addLibrary` -> `Catalog.Get` (manga must exist) -> `store.AddLibrary` (duplicate gives 409) -> 201.

**List library** (`GET /api/library`): `listLibrary` reads rows, then enriches each from the catalog with up to 8 concurrent `Get` calls under one deadline; if the catalog is down the rows still return with `catalog: unavailable` (decision D11 in the code comment).

**Login** (`internal/backend/api/router.go:login`): `store.GetByUsername` -> `auth.CheckPassword` (a dummy bcrypt compare when the user is unknown, to hide timing) -> `Manager.Issue`.

**Startup**: `cmd/backend/main.go:run`: `database.Open` -> `store.Migrate` -> `ensureAdmin` -> `auth.NewManager` -> `catalogclient.New` -> Gin server with graceful shutdown. The catalog does `database.Open` -> `catalog.Migrate` -> `SeedIfEmpty` -> gRPC server with health.

## 7. Data model
- `users` (`internal/backend/store/schema.sql`): `id`, `username` (unique, nocase), `password_hash`, `role` (`user|admin`), `created_at`
- `user_library` (same file): primary key (`user_id`, `manga_id`); `status` (`plan-to-read|reading|completed`), `current_chapter`, `updated_at`, `version`. `manga_id` has no foreign key because the catalog DB owns manga.
- `manga` (`internal/catalog/schema.sql`): `id`, `title`, `author`, `genres` (JSON array text), `status` (`ongoing|completed|hiatus`), `total_chapters`, `description`
- Separate DB files: `var/backend.db` and `var/catalog.db`

## 8. Conventions seen
- No migration tool: each program runs its embedded `schema.sql` (`CREATE TABLE IF NOT EXISTS`) at startup.
- Layering: `api` (handlers) depends on small interfaces in `api/seams.go`; `catalogclient` imports `api`, never the reverse, so handlers never import gRPC. Domain errors (`ErrCatalog*`) live in `api`; `mapCatalogErr` turns them into 404/400/409/503/502.
- Error envelope: `protocol.ErrorResponse{error, code}` with `Code*` constants, written by `api.Fail`.
- Config functions take `getenv`, so tests pass a map; `.env` is loaded only in `main`.
- Handlers take the user id from the JWT (`auth.UserID`), never from the request.
- Wire messages carry a `type` field (`protocol.Type*` constants); status values are hyphenated everywhere.
- Tests sit beside code (`*_test.go`, 18 files); temp SQLite files via `database.OpenTemp`.
- `pkg/database/db.go` (`InitDB`, `createTables`, `seedMangaIfEmpty`, `CheckDB`...) and `pkg/models` look like leftovers from an earlier design: only `test/database_test.go` uses `InitDB`/`CheckDB`, and nothing under `cmd/` or `internal/` imports `pkg/models`. (inferred: legacy)
- `.gitignore` excludes `docs/`, `var/`, `bin/`, `.env*`, `.agent/`, `AGENTS.md`, `CLAUDE.md`, `CONTEXT.md`.

## 9. Where to add things
- **New REST endpoint:** request/response types in `pkg/protocol/api.go` -> handler in `internal/backend/api/*_handlers.go` -> route in `router.go:NewRouter` -> store method in `internal/backend/store/` if it needs data -> test beside it.
- **New table or column:** edit the relevant `schema.sql`. Existing tables cannot be altered by `IF NOT EXISTS`, so this is the point where a real migration tool is needed.
- **New catalog operation:** `proto/manga.proto` -> `make proto` -> `internal/catalog/server.go` -> `catalogclient/client.go` -> add to the `api.Catalog` interface in `seams.go`.
- **TCP / UDP / WebSocket / Redis features:** implement in the stub package (`synctcp`, `notifyudp`, `chatws`, `cache`) against the matching seam (`ProgressPublisher`, `ChapterNotifier`, `CacheInvalidator`), wire it in `cmd/backend/main.go`, and use the message types in `pkg/protocol`.
- **New setting:** field and parser in `internal/config/config.go`, then `.env.example` and `compose.yaml`.

## 10. Not inspected
- `proto/mangaproto/*.pb.go` (generated), `go.sum`, `data/manga_seeds.json`
- All `*_test.go` bodies, `internal/testutil`, `internal/integration`
- `docs/`, `.agent/`, `AGENTS.md`, `CONTEXT.md` (git-ignored or not auto-read)
- Bodies of `internal/catalog/store.go` (search SQL), `internal/backend/catalogclient/client.go` (error translation), `auth.go` (JWT issue/verify), `pkg/protocol/*` validators
- `cmd/tui` beyond its first 40 lines
