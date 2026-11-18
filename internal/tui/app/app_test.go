package app

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mangahub/internal/testutil"
	"mangahub/internal/tui/client"
	"mangahub/pkg/protocol"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// fakeBackend is a tiny stateful stand-in for the real backend (user alice/Alice1234).
func fakeBackend(t *testing.T) string {
	t.Helper()
	catalog := []protocol.Manga{
		{ID: "one-piece", Title: "One Piece", Author: "Oda", Status: "ongoing", TotalChapters: 1100, Genres: []string{"Action"}, Description: "Pirates [and] rubber."},
		{ID: "naruto", Title: "Naruto", Author: "Kishimoto", Status: "completed", TotalChapters: 700, Description: "A young ninja."},
	}
	var mu sync.Mutex
	lib := map[string]*protocol.LibraryItem{}
	write := func(w http.ResponseWriter, status int, v any) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		p := r.URL.Path
		switch {
		case p == client.PathHealth:
			write(w, 200, map[string]string{"status": "ok", "catalog": "up", "redis": "disabled"})
		case p == client.PathLogin:
			var req protocol.LoginRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Username != "alice" || req.Password != "Alice1234" {
				write(w, 401, protocol.ErrorResponse{Error: "invalid username or password", Code: "unauthenticated"})
				return
			}
			write(w, 200, protocol.LoginResponse{Token: "tok", User: protocol.User{ID: "usr_1", Username: "alice", Role: "user"}})
		case p == client.PathManga:
			var items []protocol.Manga
			for _, m := range catalog {
				if strings.Contains(strings.ToLower(m.Title), r.URL.Query().Get("q")) {
					items = append(items, m)
				}
			}
			write(w, 200, protocol.SearchResponse{Items: append([]protocol.Manga{}, items...), Total: len(items), Page: 1, Limit: 20})
		case r.Header.Get("Authorization") != "Bearer tok":
			write(w, 401, protocol.ErrorResponse{Error: "missing bearer token", Code: "unauthenticated"})
		case p == client.PathLibrary && r.Method == http.MethodPost:
			var req protocol.AddLibraryRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if lib[req.MangaID] != nil {
				write(w, 409, protocol.ErrorResponse{Error: "manga already in library", Code: "conflict"})
				return
			}
			lib[req.MangaID] = &protocol.LibraryItem{MangaID: req.MangaID, Status: req.Status, Version: 1, UpdatedAt: time.Now()}
			write(w, 201, lib[req.MangaID])
		case p == client.PathLibrary:
			res := protocol.LibraryResponse{Items: []protocol.LibraryItem{}, Catalog: "ok"}
			for _, it := range lib {
				cp := *it
				cp.Manga = &catalog[0]
				res.Items = append(res.Items, cp)
			}
			write(w, 200, res)
		case strings.HasSuffix(p, "/progress"):
			var req protocol.UpdateProgressRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			it := lib[strings.Split(p, "/")[3]]
			it.CurrentChapter, it.Version = *req.CurrentChapter, it.Version+1
			write(w, 200, it)
		default:
			write(w, 404, protocol.ErrorResponse{Error: "not found", Code: "not_found"})
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

type harness struct {
	t   *testing.T
	a   *App
	sim tcell.SimulationScreen
}

func startApp(t *testing.T, base string) *harness {
	t.Helper()
	sim := tcell.NewSimulationScreen("UTF-8")
	require.NoError(t, sim.Init())
	sim.SetSize(80, 24)
	a := New(client.NewAPI(base), Addrs{}, quiet)
	a.SetScreen(sim)
	done := make(chan error, 1)
	go func() { done <- a.Run() }()
	t.Cleanup(func() {
		a.Quit()
		<-done
	})
	return &harness{t, a, sim}
}

// text reads the screen on the UI goroutine: GetContents is not locked against
// tview's Show, so reading it from the test goroutine directly is a data race.
// QueueUpdate is fine here because the test goroutine is not a tview callback.
func (h *harness) text() string {
	var b strings.Builder
	h.a.tv.QueueUpdate(func() {
		cells, w, _ := h.sim.GetContents()
		for i := 0; i < len(cells); i++ {
			if i > 0 && i%w == 0 {
				b.WriteByte('\n')
			}
			if len(cells[i].Runes) == 0 {
				b.WriteByte(' ')
				continue
			}
			b.WriteString(string(cells[i].Runes))
			if uniseg.StringWidth(string(cells[i].Runes)) == 2 {
				i++ // the next cell is the right half of a wide rune; it may hold stale runes
			}
		}
	})
	return b.String()
}

func (h *harness) waitFor(s string) {
	h.t.Helper()
	testutil.Eventually(h.t, func() bool { return strings.Contains(h.text(), s) }, 3*time.Second,
		"screen never showed "+s+"\n"+h.text())
}

func (h *harness) key(k tcell.Key)  { h.sim.InjectKey(k, 0, tcell.ModNone) }
func (h *harness) ctrl(k tcell.Key) { h.sim.InjectKey(k, 0, tcell.ModCtrl) }
func (h *harness) typeText(s string) {
	for _, r := range s {
		h.sim.InjectKey(tcell.KeyRune, r, tcell.ModNone)
	}
}

// The Gate 6 happy path, driven through the real widgets at 80×24.
func TestSmoke_LoginBrowseAddUpdate(t *testing.T) {
	h := startApp(t, fakeBackend(t))
	h.waitFor("MangaHub — Login")

	h.key(tcell.KeyTab) // password field
	h.key(tcell.KeyEnter)
	h.waitFor("Enter a username and a password") // no request for empty fields

	h.key(tcell.KeyBacktab) // focus moved to the Login button; back to password, then username
	h.key(tcell.KeyBacktab)
	h.typeText("alice")
	h.key(tcell.KeyTab)
	h.typeText("Wrong1234")
	h.key(tcell.KeyEnter)
	h.waitFor("Invalid username or password")

	h.key(tcell.KeyBacktab)
	h.ctrl(tcell.KeyCtrlU) // clear the password field
	h.typeText("Alice1234")
	h.key(tcell.KeyEnter)
	h.waitFor("alice · browser")
	h.waitFor("One Piece")
	h.waitFor("Pirates [and] rubber.") // brackets escaped, not eaten as colour tags

	h.key(tcell.KeyDown)
	h.waitFor("A young ninja.") // details follow the selection
	h.key(tcell.KeyUp)

	h.typeText("a")
	h.waitFor("Add One Piece")
	h.key(tcell.KeyTab) // dropdown → Add button
	h.key(tcell.KeyEnter)
	h.waitFor("Added to library: One Piece")
	h.typeText("a")
	h.key(tcell.KeyTab)
	h.key(tcell.KeyEnter)
	h.waitFor("Already in your library")

	h.typeText("2")
	h.waitFor("0/1100")
	h.typeText("u")
	h.waitFor("Update chapter")
	h.ctrl(tcell.KeyCtrlU)
	h.typeText("5000")
	h.key(tcell.KeyEnter)
	h.waitFor("At most 1100 chapters") // checked locally, modal stays open
	h.ctrl(tcell.KeyCtrlU)
	h.typeText("25")
	h.key(tcell.KeyEnter)
	h.waitFor("25/1100")
	h.waitFor("version 2")

	// Digits typed into a text field must not switch screens.
	h.typeText("1")
	h.waitFor("alice · browser")
	h.typeText("/")
	h.typeText("1zz")
	h.key(tcell.KeyEnter)
	h.waitFor("No manga found for '1zz'")
	assert.Contains(t, h.text(), "alice · browser")

	h.ctrl(tcell.KeyCtrlC)
	h.waitFor("Quit MangaHub?")
}

func TestSmoke_BackendDown(t *testing.T) {
	ln := testutil.ListenLoopback(t)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	h := startApp(t, "http://"+addr)
	h.waitFor("MangaHub — Login")
	h.typeText("alice")
	h.key(tcell.KeyTab)
	h.typeText("Alice1234")
	h.key(tcell.KeyEnter)
	h.waitFor("Cannot reach the backend")
	h.waitFor("HTTP ○")
}

func TestEvents_AppliedInOrder(t *testing.T) {
	h := startApp(t, fakeBackend(t))
	var got []int
	done := make(chan struct{})
	go func() { // a "network" goroutine: it only posts
		for i := range 300 {
			h.a.Post(EventFunc(func(*App) { got = append(got, i) }))
		}
		h.a.Post(EventFunc(func(*App) { close(done) }))
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("events were not applied")
	}
	require.Len(t, got, 300)
	for i, v := range got {
		require.Equal(t, i, v)
	}
}

func TestPost_DoesNotBlockAfterShutdown(t *testing.T) {
	a := New(client.NewAPI("http://127.0.0.1:1"), Addrs{}, quiet) // never run: nothing drains the channel
	for range cap(a.events) {
		a.Post(EventFunc(func(*App) {}))
	}
	a.cancel()
	returned := make(chan struct{})
	go func() { a.Post(EventFunc(func(*App) {})); close(returned) }()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("Post blocked after shutdown")
	}
}
