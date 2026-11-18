// Package integration runs the backend and the catalog together over real
// loopback gRPC. It is the only package allowed to import both sides.
package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc"
	"mangahub/data"
	"mangahub/internal/auth"
	"mangahub/internal/backend/api"
	"mangahub/internal/backend/catalogclient"
	"mangahub/internal/backend/chatws"
	"mangahub/internal/backend/notifyudp"
	"mangahub/internal/backend/store"
	"mangahub/internal/backend/synctcp"
	"mangahub/internal/catalog"
	"mangahub/internal/clock"
	"mangahub/internal/testutil"
	"mangahub/pkg/database"
	"mangahub/pkg/protocol"
	"mangahub/proto/mangaproto"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func init() { gin.SetMode(gin.TestMode) }

// serveCatalog starts the real catalog gRPC server on addr ("127.0.0.1:0" for a free port).
func serveCatalog(t *testing.T, addr string) (*grpc.Server, string) {
	t.Helper()
	return serveCatalogAt(t, addr, filepath.Join(t.TempDir(), "c.db"))
}

// serveCatalogAt is serveCatalog on a given DB file (the restart test reopens it).
func serveCatalogAt(t *testing.T, addr, dbPath string) (*grpc.Server, string) {
	t.Helper()
	db, err := database.Open(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, catalog.Migrate(t.Context(), db))
	st := catalog.NewStore(db)
	_, err = st.SeedIfEmpty(t.Context(), data.MangaSeeds)
	require.NoError(t, err)
	lis, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	srv, _ := catalog.NewGRPCServer(st, quiet)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return srv, lis.Addr().String()
}

func backend(t *testing.T, catalogAddr string, timeout time.Duration) http.Handler {
	t.Helper()
	h, _ := newBackend(t, catalogAddr, timeout, false)
	return h
}

// realtime holds the addresses of the real TCP hub and UDP notifier.
type realtime struct{ tcp, udp string }

// newBackend builds the real router with an admin account (admin / Admin12345).
// withRealtime also runs the real TCP hub, UDP notifier and chat hub (GET /ws).
func newBackend(t *testing.T, catalogAddr string, timeout time.Duration, withRealtime bool) (http.Handler, realtime) {
	t.Helper()
	return newBackendAt(t, catalogAddr, timeout, withRealtime, filepath.Join(t.TempDir(), "b.db"))
}

// newBackendAt is newBackend on a given DB file (the restart test reopens it).
func newBackendAt(t *testing.T, catalogAddr string, timeout time.Duration, withRealtime bool, dbPath string) (http.Handler, realtime) {
	t.Helper()
	cat, err := catalogclient.New(catalogAddr, timeout)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cat.Close() })
	db, err := database.Open(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, store.Migrate(t.Context(), db))
	st := store.New(db)
	hash, err := auth.HashPassword("Admin12345", bcrypt.MinCost)
	require.NoError(t, err)
	if _, err = st.CreateUser(t.Context(), "admin", hash, protocol.RoleAdmin); !errors.Is(err, store.ErrUsernameTaken) {
		require.NoError(t, err) // a reopened DB already has the admin
	}
	mgr, err := auth.NewManager("test-secret-0123456789", time.Hour)
	require.NoError(t, err)
	deps := api.Deps{Store: st, Auth: mgr, Catalog: cat, BcryptCost: bcrypt.MinCost, Log: quiet}
	var rt realtime
	if withRealtime {
		ctx, cancel := context.WithCancel(context.Background())
		ln := testutil.ListenLoopback(t)
		tcp := synctcp.NewServer(mgr, synctcp.DefaultLimits(), quiet)
		pc, err := net.ListenPacket("udp", "127.0.0.1:0")
		require.NoError(t, err)
		udp := notifyudp.NewServer(mgr, clock.System{}, quiet)
		var wg sync.WaitGroup
		wg.Go(func() { _ = tcp.Serve(ctx, ln) })
		wg.Go(func() { _ = udp.Serve(ctx, pc) })
		t.Cleanup(func() { cancel(); wg.Wait() })
		chat := chatws.NewHub(chatws.DefaultLimits(), quiet)
		t.Cleanup(chat.Close)
		deps.Publisher, deps.Notifier, deps.Chat = tcp.Hub(), udp, chat.Serve
		rt = realtime{ln.Addr().String(), pc.LocalAddr().String()}
	}
	return api.NewRouter(deps), rt
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w
}

func TestIntegration_SearchAndGet(t *testing.T) {
	_, addr := serveCatalog(t, "127.0.0.1:0")
	h := backend(t, addr, 2*time.Second)

	w := get(h, "/api/manga?q=one")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var res protocol.SearchResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Equal(t, 2, res.Total)
	assert.Len(t, res.Items, 2)

	w = get(h, "/api/manga/one-piece")
	require.Equal(t, http.StatusOK, w.Code)
	var m protocol.Manga
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
	assert.Equal(t, "one-piece", m.ID)
	assert.Positive(t, m.TotalChapters)

	assert.Equal(t, http.StatusNotFound, get(h, "/api/manga/nope").Code)
	assert.Contains(t, get(h, "/api/health").Body.String(), `"catalog":"up"`)
}

// Stopped catalog → bounded 503 (fails fast on a refused connection), then
// recovery on the same address without restarting the backend or the client.
func TestIntegration_StopThenRecover(t *testing.T) {
	const timeout = 2 * time.Second
	srv, addr := serveCatalog(t, "127.0.0.1:0")
	h := backend(t, addr, timeout)
	require.Equal(t, http.StatusOK, get(h, "/api/manga?q=one").Code)

	srv.Stop() // also closes the listener
	start := time.Now()
	w := get(h, "/api/manga?q=one")
	elapsed := time.Since(start)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
	assert.Less(t, elapsed, timeout+300*time.Millisecond)
	assert.Contains(t, get(h, "/api/health").Body.String(), `"catalog":"down"`)

	serveCatalog(t, addr)
	testutil.Eventually(t, func() bool { return get(h, "/api/manga?q=one").Code == http.StatusOK },
		5*time.Second, "backend did not recover after the catalog came back")
}

// slowCatalog never answers in time: the DeadlineExceeded path, like a frozen process.
type slowCatalog struct {
	mangaproto.UnimplementedMangaServiceServer
}

func (slowCatalog) SearchManga(ctx context.Context, _ *mangaproto.SearchMangaRequest) (*mangaproto.SearchMangaResponse, error) {
	select {
	case <-time.After(2 * time.Second):
	case <-ctx.Done():
	}
	return nil, ctx.Err()
}

func TestIntegration_SlowCatalogHitsDeadline(t *testing.T) {
	lis := testutil.ListenLoopback(t)
	srv := grpc.NewServer()
	mangaproto.RegisterMangaServiceServer(srv, slowCatalog{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	h := backend(t, lis.Addr().String(), 200*time.Millisecond)
	start := time.Now()
	w := get(h, "/api/manga?q=one")
	elapsed := time.Since(start)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.GreaterOrEqual(t, elapsed, 200*time.Millisecond)
	assert.Less(t, elapsed, 500*time.Millisecond)
}

// The backend must reach catalog data only through gRPC, never by importing it.
func TestImportGuard_BackendDoesNotImportCatalog(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "mangahub/internal/backend/...", "mangahub/cmd/backend").CombinedOutput()
	require.NoError(t, err, string(out))
	for _, pkg := range strings.Fields(string(out)) {
		assert.False(t, pkg == "mangahub/internal/catalog" || strings.HasPrefix(pkg, "mangahub/internal/catalog/"), pkg)
	}
}
