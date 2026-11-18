package api_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	"mangahub/internal/auth"
	"mangahub/internal/backend/api"
	"mangahub/internal/backend/cache"
	"mangahub/internal/backend/store"
	"mangahub/pkg/database"
	"mangahub/pkg/protocol"
)

const ttl = 30 * time.Second

func cacheEnv(t *testing.T, redisAddr string) (http.Handler, *fakeCatalog, []string) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "b.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mgr, err := auth.NewManager(secret, time.Hour)
	require.NoError(t, err)
	mc := cache.New(redisAddr, ttl, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { _ = mc.Close() })
	cat := &fakeCatalog{}
	r := api.NewRouter(api.Deps{Store: store.New(db), Auth: mgr, Catalog: cat, Cache: mc, BcryptCost: bcrypt.MinCost, Log: quiet})
	admin, _, _ := mgr.Issue("usr_a", "admin", protocol.RoleAdmin)
	return r, cat, []string{"Authorization", "Bearer " + admin}
}

func details(t *testing.T, r http.Handler) (string, protocol.Manga) {
	t.Helper()
	w := do(r, "GET", "/api/manga/one-piece", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var m protocol.Manga
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
	return w.Header().Get("X-Cache"), m
}

func TestCacheAside_MissHitExpire(t *testing.T) {
	mr := miniredis.RunT(t)
	r, cat, _ := cacheEnv(t, mr.Addr())

	state, m := details(t, r)
	assert.Equal(t, "MISS", state)
	assert.EqualValues(t, 1, cat.gets.Load())
	state, cached := details(t, r)
	assert.Equal(t, "HIT", state)
	assert.EqualValues(t, 1, cat.gets.Load(), "a HIT does not call the catalog")
	assert.Equal(t, m, cached, "the body is identical for HIT and MISS")

	assert.LessOrEqual(t, mr.TTL("manga:v1:one-piece"), ttl)
	mr.FastForward(ttl + time.Second) // miniredis TTLs move only when told to
	state, _ = details(t, r)
	assert.Equal(t, "MISS", state, "expired after the TTL")
	assert.EqualValues(t, 2, cat.gets.Load())
}

func TestCacheAside_ReleaseInvalidates(t *testing.T) {
	mr := miniredis.RunT(t)
	r, cat, admin := cacheEnv(t, mr.Addr())
	details(t, r) // warm
	require.True(t, mr.Exists("manga:v1:one-piece"))

	assert.Equal(t, http.StatusConflict, do(r, "POST", "/api/admin/manga/one-piece/release", `{"chapter":5}`, admin...).Code)
	assert.True(t, mr.Exists("manga:v1:one-piece"), "a failed release leaves the cache alone")

	require.Equal(t, http.StatusOK, do(r, "POST", "/api/admin/manga/one-piece/release", "", admin...).Code)
	assert.False(t, mr.Exists("manga:v1:one-piece"), "release deletes the key")
	state, m := details(t, r)
	assert.Equal(t, "MISS", state)
	assert.Equal(t, 1101, m.TotalChapters, "the next read carries the new total")
	fresh, _ := cat.Get(t.Context(), "one-piece")
	state, m = details(t, r)
	assert.Equal(t, "HIT", state)
	assert.Equal(t, fresh, m, "the catalog stays the source of truth")
}

func TestCacheAside_ErrorsNotCached(t *testing.T) {
	mr := miniredis.RunT(t)
	r, cat, _ := cacheEnv(t, mr.Addr())
	w := do(r, "GET", "/api/manga/nope", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Empty(t, w.Header().Get("X-Cache"))
	cat.err = api.ErrCatalogUnavailable
	w = do(r, "GET", "/api/manga/one-piece", "")
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Empty(t, w.Header().Get("X-Cache"))
	assert.Empty(t, mr.Keys(), "no error is ever cached")
}

func TestCacheAside_RedisDownFailsOpen(t *testing.T) {
	mr := miniredis.RunT(t)
	r, _, _ := cacheEnv(t, mr.Addr())
	assert.Contains(t, do(r, "GET", "/api/health", "").Body.String(), `"redis":"up"`)

	mr.Close() // Redis goes away
	start := time.Now()
	state, m := details(t, r)
	assert.Less(t, time.Since(start), 400*time.Millisecond, "the cache never makes a request slow")
	assert.Equal(t, "MISS", state)
	assert.Equal(t, "one-piece", m.ID)
	assert.Contains(t, do(r, "GET", "/api/health", "").Body.String(), `"redis":"down"`)

	start = time.Now()
	details(t, r)
	assert.Less(t, time.Since(start), 50*time.Millisecond, "during the outage Redis is skipped, not retried")

	require.NoError(t, mr.Restart())              // and comes back on the same address
	time.Sleep(time.Second + 50*time.Millisecond) // waiting out the 1 s skip window (not synchronisation)
	state, _ = details(t, r)
	assert.Equal(t, "MISS", state)
	state, _ = details(t, r)
	assert.Equal(t, "HIT", state)
}

func TestCacheAside_DisabledSendsNoHeader(t *testing.T) {
	r := catalogRouter(t, &fakeCatalog{}) // no Cache in Deps
	w := do(r, "GET", "/api/manga/one-piece", "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Header().Get("X-Cache"))
	assert.Contains(t, do(r, "GET", "/api/health", "").Body.String(), `"redis":"disabled"`)
}
