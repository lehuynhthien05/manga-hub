package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	"mangahub/internal/auth"
	"mangahub/internal/backend/api"
	"mangahub/internal/backend/store"
	"mangahub/pkg/database"
	"mangahub/pkg/protocol"
)

// spy records the release side effects in order.
type spy struct{ calls []string }

func (s *spy) Notify(ev protocol.NewChapter) int {
	s.calls = append(s.calls, "notify:"+ev.MangaID)
	return 2
}

func (s *spy) Invalidate(_ context.Context, id string) { s.calls = append(s.calls, "invalidate:"+id) }

func adminEnv(t *testing.T) (http.Handler, *fakeCatalog, *spy, map[string][]string) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "b.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mgr, err := auth.NewManager(secret, time.Hour)
	require.NoError(t, err)
	cat, sp := &fakeCatalog{}, &spy{}
	r := api.NewRouter(api.Deps{Store: store.New(db), Auth: mgr, Catalog: cat, Notifier: sp, Cache: sp,
		BcryptCost: bcrypt.MinCost, Log: quiet})
	admin, _, _ := mgr.Issue("usr_a", "admin", protocol.RoleAdmin)
	user, _, _ := mgr.Issue("usr_u", "alice", protocol.RoleUser)
	return r, cat, sp, map[string][]string{
		"admin": {"Authorization", "Bearer " + admin},
		"user":  {"Authorization", "Bearer " + user},
	}
}

func TestRelease_Endpoint(t *testing.T) {
	r, _, sp, tok := adminEnv(t)
	const path = "/api/admin/manga/one-piece/release"

	assert.Equal(t, http.StatusUnauthorized, do(r, "POST", path, "").Code)
	assert.Equal(t, http.StatusForbidden, do(r, "POST", path, "", tok["user"]...).Code)
	assert.Equal(t, http.StatusNotFound, do(r, "POST", "/api/admin/manga/nope/release", "", tok["admin"]...).Code)
	assert.Equal(t, http.StatusConflict, do(r, "POST", path, `{"chapter":1}`, tok["admin"]...).Code)
	for _, body := range []string{`{"chapter":0}`, `{"chapter":-3}`, `{"chapter":"x"}`, `{`} {
		assert.Equal(t, http.StatusBadRequest, do(r, "POST", path, body, tok["admin"]...).Code, body)
	}
	assert.Equal(t, http.StatusBadRequest, do(r, "POST", "/api/admin/manga/Bad_Id/release", "", tok["admin"]...).Code)
	assert.Empty(t, sp.calls, "no side effect on any failed release")

	w := do(r, "POST", path, "", tok["admin"]...) // empty body: next chapter
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var res protocol.ReleaseResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Equal(t, 1101, res.Chapter)
	assert.Equal(t, 2, res.UDPSent)
	assert.Equal(t, []string{"invalidate:one-piece", "notify:one-piece"}, sp.calls, "invalidate before notify")

	w = do(r, "POST", path, `{"chapter":1200}`, tok["admin"]...)
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Equal(t, 1200, res.Manga.TotalChapters)
}

func TestRelease_CatalogDownNoUDP(t *testing.T) {
	r, cat, sp, tok := adminEnv(t)
	cat.err = api.ErrCatalogUnavailable
	for _, body := range []string{"", `{"chapter":1200}`} {
		assert.Equal(t, http.StatusServiceUnavailable, do(r, "POST", "/api/admin/manga/one-piece/release", body, tok["admin"]...).Code)
	}
	assert.Empty(t, sp.calls)
}
