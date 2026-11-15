package catalog_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mangahub/data"
	"mangahub/internal/catalog"
	"mangahub/pkg/database"
)

func seededStore(t *testing.T) (*catalog.Store, *sql.DB) {
	t.Helper()
	db := database.OpenTemp(t)
	require.NoError(t, catalog.Migrate(context.Background(), db))
	s := catalog.NewStore(db)
	n, err := s.SeedIfEmpty(context.Background(), data.MangaSeeds)
	require.NoError(t, err)
	require.Equal(t, 35, n)
	return s, db
}

func TestSeedIfEmpty_InsertsThenNoops(t *testing.T) {
	s, db := seededStore(t)
	_, err := db.Exec(`UPDATE manga SET total_chapters = 9999 WHERE id = 'one-piece'`)
	require.NoError(t, err)

	n, err := s.SeedIfEmpty(context.Background(), data.MangaSeeds)
	require.NoError(t, err)
	assert.Zero(t, n)
	m, err := s.Get(context.Background(), "one-piece")
	require.NoError(t, err)
	assert.Equal(t, 9999, m.TotalChapters, "reseed must not overwrite")
}

func TestSearch(t *testing.T) {
	s, _ := seededStore(t)
	ids := func(ms []catalog.Manga) []string {
		out := []string{}
		for _, m := range ms {
			out = append(out, m.ID)
		}
		return out
	}
	ctx := context.Background()

	items, total, err := s.Search(ctx, catalog.SearchParams{Query: "ONE"})
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	assert.ElementsMatch(t, []string{"one-piece", "one-punch-man"}, ids(items))

	items, _, err = s.Search(ctx, catalog.SearchParams{Query: "oda"}) // author match
	require.NoError(t, err)
	assert.Contains(t, ids(items), "one-piece")

	items, total, err = s.Search(ctx, catalog.SearchParams{Query: "%"}) // literal, not a wildcard
	require.NoError(t, err)
	assert.Zero(t, total)
	assert.NotNil(t, items)

	items, _, err = s.Search(ctx, catalog.SearchParams{Genre: "seinen"})
	require.NoError(t, err)
	assert.NotEmpty(t, items)
	for _, m := range items {
		assert.Contains(t, m.Genres, "Seinen")
	}

	items, _, err = s.Search(ctx, catalog.SearchParams{Status: "hiatus"})
	require.NoError(t, err)
	assert.Len(t, items, 2)

	items, total, err = s.Search(ctx, catalog.SearchParams{Limit: 5, Offset: 5})
	require.NoError(t, err)
	assert.Equal(t, 35, total)
	assert.Len(t, items, 5)

	_, _, err = s.Search(ctx, catalog.SearchParams{Limit: -1})
	assert.ErrorIs(t, err, catalog.ErrInvalid)
}

func TestGet_FoundAndMissing(t *testing.T) {
	s, _ := seededStore(t)
	m, err := s.Get(context.Background(), "one-piece")
	require.NoError(t, err)
	assert.Equal(t, 1100, m.TotalChapters)
	assert.NotEmpty(t, m.Genres)

	_, err = s.Get(context.Background(), "nope")
	assert.ErrorIs(t, err, catalog.ErrNotFound)
}

func TestUpdateChapters(t *testing.T) {
	s, _ := seededStore(t)
	ctx := context.Background()

	m, prev, err := s.UpdateChapters(ctx, "one-piece", 1101)
	require.NoError(t, err)
	assert.Equal(t, 1100, prev)
	assert.Equal(t, 1101, m.TotalChapters)

	for name, tc := range map[string]struct {
		id    string
		total int
		want  error
	}{
		"not found": {"nope", 5, catalog.ErrNotFound},
		"lower":     {"one-piece", 1000, catalog.ErrNotIncreasing},
		"equal":     {"one-piece", 1101, catalog.ErrNotIncreasing},
		"zero":      {"one-piece", 0, catalog.ErrInvalid},
		"empty id":  {"", 5, catalog.ErrInvalid},
	} {
		_, _, err := s.UpdateChapters(ctx, tc.id, tc.total)
		assert.ErrorIs(t, err, tc.want, name)
	}
}

func TestUpdateChapters_ConcurrentKeepsMax(t *testing.T) {
	s, _ := seededStore(t)
	const base = 2000
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := s.UpdateChapters(context.Background(), "one-piece", base+i)
			if err != nil && !errors.Is(err, catalog.ErrNotIncreasing) {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	m, err := s.Get(context.Background(), "one-piece")
	require.NoError(t, err)
	assert.Equal(t, base+49, m.TotalChapters)
}

func TestPersistence_ReopenKeepsDataAndSkipsSeed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.db")
	db, err := database.Open(path)
	require.NoError(t, err)
	require.NoError(t, catalog.Migrate(context.Background(), db))
	s := catalog.NewStore(db)
	_, err = s.SeedIfEmpty(context.Background(), data.MangaSeeds)
	require.NoError(t, err)
	_, _, err = s.UpdateChapters(context.Background(), "berserk", 400)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	db, err = database.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	s = catalog.NewStore(db)
	n, err := s.SeedIfEmpty(context.Background(), data.MangaSeeds)
	require.NoError(t, err)
	assert.Zero(t, n)
	m, err := s.Get(context.Background(), "berserk")
	require.NoError(t, err)
	assert.Equal(t, 400, m.TotalChapters)
}
