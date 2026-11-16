package store_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mangahub/internal/backend/store"
	"mangahub/pkg/database"
)

func libStore(t *testing.T, path string) *store.Store {
	t.Helper()
	db, err := database.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, store.Migrate(t.Context(), db))
	st := store.New(db)
	for _, u := range []string{"alice", "bob"} {
		if _, err := st.GetByUsername(t.Context(), u); err != nil {
			_, err := st.CreateUser(t.Context(), u, "h", "user")
			require.NoError(t, err)
		}
	}
	return st
}

func uid(t *testing.T, st *store.Store, name string) string {
	u, err := st.GetByUsername(t.Context(), name)
	require.NoError(t, err)
	return u.ID
}

func TestLibrary_AddDuplicateAndOtherUser(t *testing.T) {
	st := libStore(t, filepath.Join(t.TempDir(), "b.db"))
	a, b := uid(t, st, "alice"), uid(t, st, "bob")

	it, err := st.AddLibrary(t.Context(), a, "one-piece", "reading", 3)
	require.NoError(t, err)
	assert.Equal(t, 1, it.Version)
	assert.Equal(t, 3, it.CurrentChapter)
	assert.False(t, it.UpdatedAt.IsZero())

	_, err = st.AddLibrary(t.Context(), a, "one-piece", "reading", 0)
	assert.ErrorIs(t, err, store.ErrAlreadyInLibrary)
	_, err = st.AddLibrary(t.Context(), b, "one-piece", "reading", 0)
	assert.NoError(t, err, "the same manga for another user is fine")
}

func TestLibrary_ListGetUpdate(t *testing.T) {
	st := libStore(t, filepath.Join(t.TempDir(), "b.db"))
	a, b := uid(t, st, "alice"), uid(t, st, "bob")

	list, err := st.ListLibrary(t.Context(), a)
	require.NoError(t, err)
	assert.NotNil(t, list)
	assert.Empty(t, list)

	_, err = st.AddLibrary(t.Context(), a, "naruto", "plan-to-read", 0)
	require.NoError(t, err)
	_, err = st.GetLibrary(t.Context(), b, "naruto")
	assert.ErrorIs(t, err, store.ErrNotInLibrary)

	ch := 10
	it, err := st.UpdateProgress(t.Context(), a, "naruto", &ch, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, it.Version)
	assert.Equal(t, "plan-to-read", it.Status)

	s := "reading"
	it, err = st.UpdateProgress(t.Context(), a, "naruto", nil, &s)
	require.NoError(t, err)
	assert.Equal(t, 3, it.Version)
	assert.Equal(t, 10, it.CurrentChapter, "status-only update keeps the chapter")
	assert.Equal(t, "reading", it.Status)

	_, err = st.UpdateProgress(t.Context(), b, "naruto", &ch, nil)
	assert.ErrorIs(t, err, store.ErrNotInLibrary, "bob cannot update alice's row")

	list, err = st.ListLibrary(t.Context(), b)
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestLibrary_Persistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "b.db")
	st := libStore(t, path)
	a := uid(t, st, "alice")
	_, err := st.AddLibrary(t.Context(), a, "one-piece", "reading", 0)
	require.NoError(t, err)
	ch := 25
	_, err = st.UpdateProgress(t.Context(), a, "one-piece", &ch, nil)
	require.NoError(t, err)
	before, err := st.ListLibrary(t.Context(), a)
	require.NoError(t, err)

	after, err := libStore(t, path).ListLibrary(t.Context(), a) // a second pool on the same file = a restart
	require.NoError(t, err)
	assert.Equal(t, before, after)
	assert.Equal(t, 2, after[0].Version)
}
