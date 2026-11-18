package app

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"mangahub/pkg/protocol"
)

func TestLibraryModel_ApplyProgress(t *testing.T) {
	m := NewLibraryModel()
	m.Replace([]protocol.LibraryItem{{MangaID: "one-piece", CurrentChapter: 10, Version: 2, Status: "reading"}})

	assert.False(t, m.ApplyProgress(protocol.ProgressUpdated{MangaID: "one-piece", Chapter: 99, Version: 2}), "same version")
	assert.False(t, m.ApplyProgress(protocol.ProgressUpdated{MangaID: "one-piece", Chapter: 99, Version: 1}), "older")
	assert.False(t, m.ApplyProgress(protocol.ProgressUpdated{MangaID: "naruto", Chapter: 1, Version: 9}), "unknown manga")
	it, _ := m.Get("one-piece")
	assert.Equal(t, 10, it.CurrentChapter)

	assert.True(t, m.ApplyProgress(protocol.ProgressUpdated{MangaID: "one-piece", Chapter: 0, Version: 3}))
	it, _ = m.Get("one-piece")
	assert.Equal(t, 0, it.CurrentChapter, "chapter 0 is valid")
	assert.Equal(t, "reading", it.Status, "empty status keeps the old one")
}

func TestLibraryModel_UpsertKeepsCatalogData(t *testing.T) {
	m := NewLibraryModel()
	op := &protocol.Manga{ID: "one-piece", TotalChapters: 1100}
	m.Replace([]protocol.LibraryItem{{MangaID: "one-piece", Version: 1, Manga: op}})
	m.Upsert(protocol.LibraryItem{MangaID: "one-piece", CurrentChapter: 25, Version: 2})
	it, _ := m.Get("one-piece")
	assert.Equal(t, "25/1100", progress(it))
	m.Upsert(protocol.LibraryItem{MangaID: "one-piece", CurrentChapter: 3, Version: 1})
	it, _ = m.Get("one-piece")
	assert.Equal(t, 25, it.CurrentChapter, "an older response never wins")

	m.Upsert(protocol.LibraryItem{MangaID: "naruto", CurrentChapter: 12})
	assert.Equal(t, "naruto", m.Items()[0].MangaID, "new entries go first")
	assert.Equal(t, "12/?", progress(m.Items()[0]))
}

func TestAge(t *testing.T) {
	now := time.Now()
	for d, want := range map[time.Duration]string{
		10 * time.Second: "just now", 3 * time.Minute: "3 min", 5 * time.Hour: "5 h", 49 * time.Hour: "2 d",
	} {
		assert.Equal(t, want, age(now.Add(-d), now))
	}
}
