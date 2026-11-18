package api_test

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
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

type published struct {
	user, origin string
	ev           protocol.ProgressUpdated
}

type spyPublisher struct {
	mu     sync.Mutex
	events []published
}

func (s *spyPublisher) Publish(user string, ev protocol.ProgressUpdated, origin string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, published{user, origin, ev})
}

func (s *spyPublisher) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

type libEnv struct {
	r   http.Handler
	cat *fakeCatalog
	pub *spyPublisher
}

func newLibEnv(t *testing.T, path string) *libEnv {
	t.Helper()
	db, err := database.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, store.Migrate(t.Context(), db))
	mgr, err := auth.NewManager(secret, time.Hour)
	require.NoError(t, err)
	e := &libEnv{cat: &fakeCatalog{}, pub: &spyPublisher{}}
	e.r = api.NewRouter(api.Deps{Store: store.New(db), Auth: mgr, Catalog: e.cat, Publisher: e.pub,
		BcryptCost: bcrypt.MinCost, Log: quiet})
	return e
}

// loginAs registers name (if new) and returns a bearer header pair.
func (e *libEnv) loginAs(t *testing.T, name string) []string {
	t.Helper()
	body := `{"username":"` + name + `","password":"Passw0rdX"}`
	do(e.r, "POST", "/api/auth/register", body)
	w := do(e.r, "POST", "/api/auth/login", body)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var lr protocol.LoginResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &lr))
	return []string{"Authorization", "Bearer " + lr.Token}
}

func item(t *testing.T, body []byte) protocol.LibraryItem {
	t.Helper()
	var it protocol.LibraryItem
	require.NoError(t, json.Unmarshal(body, &it), string(body))
	return it
}

func TestLibrary_RequiresAuth401(t *testing.T) {
	e := newLibEnv(t, filepath.Join(t.TempDir(), "b.db"))
	for _, rt := range [][2]string{{"POST", "/api/library"}, {"GET", "/api/library"}, {"PUT", "/api/library/one-piece/progress"}} {
		assert.Equal(t, http.StatusUnauthorized, do(e.r, rt[0], rt[1], `{}`).Code, rt[1])
		assert.Equal(t, http.StatusUnauthorized, do(e.r, rt[0], rt[1], `{}`, "Authorization", "Bearer forged.x.y").Code, rt[1])
	}
}

func TestLibrary_AddValidation(t *testing.T) {
	e := newLibEnv(t, filepath.Join(t.TempDir(), "b.db"))
	a := e.loginAs(t, "alice")

	w := do(e.r, "POST", "/api/library", `{"manga_id":"one-piece"}`, a...)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	it := item(t, w.Body.Bytes())
	assert.Equal(t, protocol.StatusPlanToRead, it.Status, "status defaults to plan-to-read")
	assert.Equal(t, 1, it.Version)
	require.NotNil(t, it.Manga)
	assert.Equal(t, 1100, it.Manga.TotalChapters)

	assert.Equal(t, http.StatusConflict, do(e.r, "POST", "/api/library", `{"manga_id":"one-piece"}`, a...).Code)
	assert.Equal(t, http.StatusNotFound, do(e.r, "POST", "/api/library", `{"manga_id":"nope"}`, a...).Code)
	for _, body := range []string{`{"manga_id":"x","status":"on-hold"}`, `{"manga_id":"Bad Id"}`, `{}`, `{"manga_id":5}`} {
		assert.Equal(t, http.StatusBadRequest, do(e.r, "POST", "/api/library", body, a...).Code, body)
	}
	e.cat.err = api.ErrCatalogUnavailable
	assert.Equal(t, http.StatusServiceUnavailable, do(e.r, "POST", "/api/library", `{"manga_id":"naruto"}`, a...).Code)
}

func TestLibrary_Isolation(t *testing.T) {
	e := newLibEnv(t, filepath.Join(t.TempDir(), "b.db"))
	a, b := e.loginAs(t, "alice"), e.loginAs(t, "bob")
	require.Equal(t, http.StatusCreated, do(e.r, "POST", "/api/library", `{"manga_id":"one-piece","status":"reading"}`, a...).Code)

	assert.JSONEq(t, `{"items":[],"catalog":"ok"}`, do(e.r, "GET", "/api/library", "", b...).Body.String())
	assert.Equal(t, http.StatusNotFound, do(e.r, "PUT", "/api/library/one-piece/progress", `{"current_chapter":9}`, b...).Code)

	var lr protocol.LibraryResponse
	require.NoError(t, json.Unmarshal(do(e.r, "GET", "/api/library", "", a...).Body.Bytes(), &lr))
	require.Len(t, lr.Items, 1)
	assert.Equal(t, 0, lr.Items[0].CurrentChapter)
	assert.Equal(t, 1, lr.Items[0].Version, "bob's attempt did not touch alice's row")
	assert.Zero(t, e.pub.count())
}

func TestProgress_ValidationAndPublish(t *testing.T) {
	e := newLibEnv(t, filepath.Join(t.TempDir(), "b.db"))
	a := e.loginAs(t, "alice")
	require.Equal(t, http.StatusCreated, do(e.r, "POST", "/api/library", `{"manga_id":"one-piece"}`, a...).Code)
	put := func(body string, hdr ...string) int {
		return do(e.r, "PUT", "/api/library/one-piece/progress", body, append(a, hdr...)...).Code
	}

	for _, body := range []string{`{"current_chapter":-1}`, `{"current_chapter":1101}`, `{"current_chapter":"abc"}`,
		`{"current_chapter":12.5}`, ``, `{}`, `{"status":"dropped"}`} {
		assert.Equal(t, http.StatusBadRequest, put(body), body)
	}
	w := do(e.r, "PUT", "/api/library/one-piece/progress", `{"current_chapter":5000}`, a...)
	assert.Contains(t, w.Body.String(), "1100", "the message names the limit")
	assert.Equal(t, http.StatusNotFound, do(e.r, "PUT", "/api/library/naruto/progress", `{"current_chapter":1}`, a...).Code)
	e.cat.err = api.ErrCatalogUnavailable
	assert.Equal(t, http.StatusServiceUnavailable, put(`{"current_chapter":1}`))
	e.cat.err = nil
	assert.Zero(t, e.pub.count(), "no event for a rejected update")

	w = do(e.r, "PUT", "/api/library/one-piece/progress", `{"current_chapter":25,"status":"reading"}`, append(a, "X-Session-ID", "sess-1")...)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, 2, item(t, w.Body.Bytes()).Version)
	require.Equal(t, 1, e.pub.count())
	got := e.pub.events[0]
	assert.Equal(t, "sess-1", got.origin)
	assert.NotEmpty(t, got.user)
	assert.Equal(t, protocol.ProgressUpdated{Type: "progress_updated", MangaID: "one-piece", Chapter: 25, Version: 2, Status: "reading"}, got.ev)

	// Status-only update skips the catalog, keeps the chapter and bumps the version; chapter 0 is valid.
	e.cat.err = api.ErrCatalogUnavailable
	w = do(e.r, "PUT", "/api/library/one-piece/progress", `{"status":"completed"}`, a...)
	require.Equal(t, http.StatusOK, w.Code)
	it := item(t, w.Body.Bytes())
	assert.Equal(t, 25, it.CurrentChapter)
	assert.Equal(t, 3, it.Version)
	e.cat.err = nil
	assert.Equal(t, http.StatusOK, put(`{"current_chapter":0}`))
}

func TestProgress_ConcurrentVersions(t *testing.T) {
	e := newLibEnv(t, filepath.Join(t.TempDir(), "b.db"))
	a := e.loginAs(t, "alice")
	require.Equal(t, http.StatusCreated, do(e.r, "POST", "/api/library", `{"manga_id":"one-piece"}`, a...).Code)

	const n = 20
	versions := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			w := do(e.r, "PUT", "/api/library/one-piece/progress", `{"current_chapter":`+strconv.Itoa(i)+`}`, a...)
			if w.Code == http.StatusOK {
				var it protocol.LibraryItem
				_ = json.Unmarshal(w.Body.Bytes(), &it)
				versions[i] = it.Version
			}
		})
	}
	wg.Wait()
	sort.Ints(versions)
	for i, v := range versions {
		assert.Equal(t, i+2, v, "versions must be exactly 2..21, unique and gap-free")
	}
	assert.Equal(t, n, e.pub.count())
}

func TestLibrary_CatalogDownDegrades(t *testing.T) {
	e := newLibEnv(t, filepath.Join(t.TempDir(), "b.db"))
	a := e.loginAs(t, "alice")
	for _, id := range []string{"one-piece", "naruto", "bleach"} {
		require.Equal(t, http.StatusCreated, do(e.r, "POST", "/api/library", `{"manga_id":"`+id+`"}`, a...).Code)
	}
	var lr protocol.LibraryResponse
	require.NoError(t, json.Unmarshal(do(e.r, "GET", "/api/library", "", a...).Body.Bytes(), &lr))
	assert.Equal(t, protocol.CatalogOK, lr.Catalog)
	require.Len(t, lr.Items, 3)
	for _, it := range lr.Items {
		require.NotNil(t, it.Manga, it.MangaID)
		assert.Equal(t, it.MangaID, it.Manga.ID)
	}

	e.cat.err = api.ErrCatalogUnavailable
	w := do(e.r, "GET", "/api/library", "", a...)
	require.Equal(t, http.StatusOK, w.Code)
	var down protocol.LibraryResponse // fresh: decoding into lr would keep its old Manga pointers
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &down))
	assert.Equal(t, protocol.CatalogUnavailable, down.Catalog)
	require.Len(t, down.Items, 3, "rows are returned even with the catalog down")
	for _, it := range down.Items {
		assert.Nil(t, it.Manga)
	}
}

func TestLibrary_PersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "b.db")
	e := newLibEnv(t, path)
	a := e.loginAs(t, "alice")
	require.Equal(t, http.StatusCreated, do(e.r, "POST", "/api/library", `{"manga_id":"one-piece"}`, a...).Code)
	require.Equal(t, http.StatusOK, do(e.r, "PUT", "/api/library/one-piece/progress", `{"current_chapter":25}`, a...).Code)

	e2 := newLibEnv(t, path) // same file, same secret: the old token still verifies
	var lr protocol.LibraryResponse
	require.NoError(t, json.Unmarshal(do(e2.r, "GET", "/api/library", "", a...).Body.Bytes(), &lr))
	require.Len(t, lr.Items, 1)
	assert.Equal(t, 25, lr.Items[0].CurrentChapter)
	assert.Equal(t, 2, lr.Items[0].Version)
}

// Property: for a random number N of concurrent updates on one entry, the returned
// versions are exactly {2..N+1} and the stored version ends at N+1.
func TestProgress_VersionsGapFreeUnderConcurrency(t *testing.T) {
	rng := rand.New(rand.NewPCG(2026, 11)) // seeded so a failure can be replayed
	for round := range 5 {
		n := 1 + rng.IntN(50)
		t.Run(fmt.Sprintf("round%d_N%d", round, n), func(t *testing.T) {
			e := newLibEnv(t, filepath.Join(t.TempDir(), "b.db"))
			a := e.loginAs(t, "alice")
			require.Equal(t, http.StatusCreated, do(e.r, "POST", "/api/library", `{"manga_id":"one-piece"}`, a...).Code)
			versions := make([]int, n)
			var wg sync.WaitGroup
			for i := range n {
				wg.Go(func() {
					w := do(e.r, "PUT", "/api/library/one-piece/progress", `{"current_chapter":`+strconv.Itoa(i)+`}`, a...)
					var it protocol.LibraryItem
					if assert.Equal(t, http.StatusOK, w.Code) && json.Unmarshal(w.Body.Bytes(), &it) == nil {
						versions[i] = it.Version
					}
				})
			}
			wg.Wait()
			sort.Ints(versions)
			for i, v := range versions {
				require.Equal(t, i+2, v, "gap-free and unique")
			}
			var lr protocol.LibraryResponse
			require.NoError(t, json.Unmarshal(do(e.r, "GET", "/api/library", "", a...).Body.Bytes(), &lr))
			assert.Equal(t, n+1, lr.Items[0].Version, "stored version = N+1")
		})
	}
}
