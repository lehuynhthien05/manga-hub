package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
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

// fakeCatalog returns err when set, otherwise canned data; it records the last search,
// counts Get calls and keeps the total a successful release sets (1100 at first).
type fakeCatalog struct {
	err     error
	healthy bool
	last    api.SearchParams
	items   []protocol.Manga
	gets    atomic.Int64
	total   int
}

func (f *fakeCatalog) current() int {
	if f.total == 0 {
		return 1100
	}
	return f.total
}

func (f *fakeCatalog) Search(_ context.Context, p api.SearchParams) (protocol.SearchResponse, error) {
	f.last = p
	if f.err != nil {
		return protocol.SearchResponse{}, f.err
	}
	return protocol.SearchResponse{Items: append([]protocol.Manga{}, f.items...), Total: len(f.items)}, nil
}

func (f *fakeCatalog) Get(_ context.Context, id string) (protocol.Manga, error) {
	f.gets.Add(1)
	if f.err != nil {
		return protocol.Manga{}, f.err
	}
	if id == "nope" {
		return protocol.Manga{}, api.ErrCatalogNotFound
	}
	return protocol.Manga{ID: id, Title: "One Piece", Genres: []string{}, TotalChapters: f.current()}, nil
}

// UpdateChapters behaves like the catalog: the new total must be higher than the current one.
func (f *fakeCatalog) UpdateChapters(_ context.Context, id string, total int) (protocol.Manga, int, error) {
	switch {
	case f.err != nil:
		return protocol.Manga{}, 0, f.err
	case id == "nope":
		return protocol.Manga{}, 0, api.ErrCatalogNotFound
	case total <= f.current():
		return protocol.Manga{}, 0, api.ErrCatalogConflict
	}
	prev := f.current()
	f.total = total
	return protocol.Manga{ID: id, Title: "One Piece", Genres: []string{}, TotalChapters: total}, prev, nil
}

func (f *fakeCatalog) Healthy(context.Context) bool { return f.healthy }

func catalogRouter(t *testing.T, f *fakeCatalog) http.Handler {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "b.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mgr, err := auth.NewManager(secret, time.Hour)
	require.NoError(t, err)
	return api.NewRouter(api.Deps{Store: store.New(db), Auth: mgr, Catalog: f, BcryptCost: bcrypt.MinCost, Log: quiet})
}

func TestSearchManga_HappyAndEmpty(t *testing.T) {
	f := &fakeCatalog{items: []protocol.Manga{{ID: "one-piece"}, {ID: "one-punch-man"}}}
	r := catalogRouter(t, f)

	w := do(r, "GET", "/api/manga?q=%20one%20&limit=5&page=3&status=ongoing&genre=Action", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var res protocol.SearchResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Len(t, res.Items, 2)
	assert.Equal(t, 2, res.Total)
	assert.Equal(t, 3, res.Page)
	assert.Equal(t, 5, res.Limit)
	assert.Equal(t, api.SearchParams{Query: "one", Genre: "Action", Status: "ongoing", Limit: 5, Offset: 10}, f.last)

	f.items = nil
	w = do(r, "GET", "/api/manga?q=zzzz", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"items":[]`)
	assert.Contains(t, w.Body.String(), `"page":1,"limit":20`)
}

func TestSearchManga_InvalidQuery(t *testing.T) {
	r := catalogRouter(t, &fakeCatalog{})
	for query, param := range map[string]string{
		"limit=0":                          "limit",
		"limit=101":                        "limit",
		"page=0":                           "page",
		"page=x":                           "page",
		"q=" + strings.Repeat("é", 101):    "q",
		"status=bogus":                     "status",
		"genre=" + strings.Repeat("g", 41): "genre",
	} {
		w := do(r, "GET", "/api/manga?"+query, "")
		assert.Equal(t, http.StatusBadRequest, w.Code, query)
		var e protocol.ErrorResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &e))
		assert.Equal(t, protocol.CodeInvalidInput, e.Code, query)
		assert.True(t, strings.HasPrefix(e.Error, param+":"), "%s: %s", query, e.Error)
	}
}

func TestGetManga(t *testing.T) {
	r := catalogRouter(t, &fakeCatalog{})
	w := do(r, "GET", "/api/manga/one-piece", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"id":"one-piece"`)

	for _, bad := range []string{"One-Piece", "a_b", strings.Repeat("a", 65)} {
		assert.Equal(t, http.StatusBadRequest, do(r, "GET", "/api/manga/"+bad, "").Code, bad)
	}
}

func TestCatalogErrors_Mapped(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{api.ErrCatalogNotFound, http.StatusNotFound, protocol.CodeNotFound},
		{api.ErrCatalogInvalid, http.StatusBadRequest, protocol.CodeInvalidInput},
		{api.ErrCatalogConflict, http.StatusConflict, protocol.CodeConflict},
		{api.ErrCatalogUnavailable, http.StatusServiceUnavailable, protocol.CodeCatalogUnavailable},
		{errors.New("rpc error: secret internals"), http.StatusBadGateway, protocol.CodeBadGateway},
	}
	for _, tc := range cases {
		r := catalogRouter(t, &fakeCatalog{err: tc.err})
		for _, path := range []string{"/api/manga?q=x", "/api/manga/one-piece"} {
			w := do(r, "GET", path, "")
			assert.Equal(t, tc.status, w.Code, "%v %s", tc.err, path)
			assert.Equal(t, tc.code, code(t, w))
			assert.NotContains(t, w.Body.String(), "secret internals", "no internal error text leaks")
		}
	}
}

func TestHealth_ReportsCatalog(t *testing.T) {
	f := &fakeCatalog{}
	r := catalogRouter(t, f)
	assert.Contains(t, do(r, "GET", "/api/health", "").Body.String(), `"catalog":"down"`)
	f.healthy = true
	w := do(r, "GET", "/api/health", "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"catalog":"up"`)
}
