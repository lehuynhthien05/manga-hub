package app

import (
	"fmt"
	"time"

	"mangahub/pkg/protocol"
)

// LibraryModel is the single source the Library table renders from. HTTP results
// (now) and TCP progress events (Phase 7) both change it; it is used on the UI goroutine only.
type LibraryModel struct {
	items map[string]*protocol.LibraryItem
	order []string
}

func NewLibraryModel() *LibraryModel { return &LibraryModel{items: map[string]*protocol.LibraryItem{}} }

func (m *LibraryModel) Replace(items []protocol.LibraryItem) {
	m.items, m.order = map[string]*protocol.LibraryItem{}, nil
	for i := range items {
		m.items[items[i].MangaID] = &items[i]
		m.order = append(m.order, items[i].MangaID)
	}
}

// Upsert stores an item returned by the server, keeping the known catalog data
// when the response has none; an older version never overwrites a newer one.
func (m *LibraryModel) Upsert(it protocol.LibraryItem) {
	old, ok := m.items[it.MangaID]
	if !ok {
		m.items[it.MangaID] = &it
		m.order = append([]string{it.MangaID}, m.order...)
		return
	}
	if it.Version < old.Version {
		return
	}
	if it.Manga == nil {
		it.Manga = old.Manga
	}
	*old = it
}

// ApplyProgress ignores events that are not newer than what we know (TCP may
// deliver an echo of our own HTTP result, or arrive out of order).
func (m *LibraryModel) ApplyProgress(ev protocol.ProgressUpdated) (changed bool) {
	it, ok := m.items[ev.MangaID]
	if !ok || ev.Version <= it.Version {
		return false
	}
	it.CurrentChapter, it.Version = ev.Chapter, ev.Version
	if ev.Status != "" {
		it.Status = ev.Status
	}
	return true
}

func (m *LibraryModel) Get(id string) (protocol.LibraryItem, bool) {
	it, ok := m.items[id]
	if !ok {
		return protocol.LibraryItem{}, false
	}
	return *it, true
}

func (m *LibraryModel) Items() []protocol.LibraryItem {
	out := make([]protocol.LibraryItem, 0, len(m.order))
	for _, id := range m.order {
		out = append(out, *m.items[id])
	}
	return out
}

// age renders a timestamp relative to now: "just now", "3 min", "5 h", "2 d".
func age(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h", int(d.Hours()))
	default:
		return fmt.Sprintf("%d d", int(d.Hours()/24))
	}
}

// progress renders "12/100", or "12/?" when the catalog data is unknown.
func progress(it protocol.LibraryItem) string {
	if it.Manga == nil {
		return fmt.Sprintf("%d/?", it.CurrentChapter)
	}
	return fmt.Sprintf("%d/%d", it.CurrentChapter, it.Manga.TotalChapters)
}
