package integration_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
	"github.com/stretchr/testify/require"
	"mangahub/internal/testutil"
	"mangahub/internal/tui/app"
	"mangahub/internal/tui/client"
)

// screen drives the real TUI on a simulation screen. Contents are read on the UI
// goroutine (QueueUpdate from this non-callback goroutine) to avoid a race with drawing.
type screen struct {
	t   *testing.T
	a   *app.App
	sim tcell.SimulationScreen
}

func (s *screen) text() string {
	var b strings.Builder
	s.a.QueueUpdate(func() {
		cells, w, _ := s.sim.GetContents()
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

func (s *screen) waitFor(want string) {
	s.t.Helper()
	testutil.Eventually(s.t, func() bool { return strings.Contains(s.text(), want) }, 5*time.Second,
		"screen never showed "+want+"\n"+s.text())
}

func (s *screen) keys(text string) {
	for _, r := range text {
		s.sim.InjectKey(tcell.KeyRune, r, tcell.ModNone)
	}
}

func (s *screen) key(k tcell.Key, mod tcell.ModMask) { s.sim.InjectKey(k, 0, mod) }

// startTUI runs a real TUI on its own 80×24 simulation screen.
func startTUI(t *testing.T, apiURL, tcpAddr, udpAddr string) *screen {
	t.Helper()
	sim := tcell.NewSimulationScreen("UTF-8")
	require.NoError(t, sim.Init())
	sim.SetSize(80, 24)
	a := app.New(client.NewAPI(apiURL), tcpAddr, udpAddr, quiet)
	a.SetScreen(sim)
	done := make(chan error, 1)
	go func() { done <- a.Run() }()
	t.Cleanup(func() { a.Quit(); <-done })
	return &screen{t, a, sim}
}

func (s *screen) login(user, pw string) {
	s.t.Helper()
	s.waitFor("MangaHub — Login")
	s.keys(user)
	s.key(tcell.KeyTab, 0)
	s.keys(pw)
	s.key(tcell.KeyEnter, 0)
	s.waitFor(user + " · browser")
}

// Gate 6 steps 5–12 against the real backend router and the real catalog over gRPC.
func TestTUI_AgainstRealBackend(t *testing.T) {
	cat, addr := serveCatalog(t, "127.0.0.1:0")
	srv := httptest.NewServer(backend(t, addr, 2*time.Second))
	t.Cleanup(srv.Close)
	resp, err := http.Post(srv.URL+client.PathRegister, "application/json",
		strings.NewReader(`{"username":"alice","password":"Alice1234"}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	_ = resp.Body.Close()

	s := startTUI(t, srv.URL, "", "")
	s.waitFor("MangaHub — Login")
	s.keys("alice")
	s.key(tcell.KeyTab, 0)
	s.keys("Alice1234")
	s.key(tcell.KeyEnter, 0)
	s.waitFor("alice · browser")

	s.keys("/one")
	s.key(tcell.KeyEnter, 0)
	s.waitFor("Results · 2 · page 1/1") // wait for the "one" search, not the initial full listing
	s.waitFor("One Piece (ワンピース)")
	s.waitFor("One Punch Man")

	s.keys("a")
	s.key(tcell.KeyTab, 0)
	s.key(tcell.KeyEnter, 0)
	s.waitFor("Added to library")

	s.keys("2")
	s.waitFor("0/1100")
	s.keys("u")
	s.key(tcell.KeyCtrlU, tcell.ModCtrl)
	s.keys("25")
	s.key(tcell.KeyEnter, 0)
	s.waitFor("25/1100")
	s.waitFor("version 2")

	cat.Stop() // Gate 6.12: the catalog goes away
	s.keys("1/zz")
	s.key(tcell.KeyEnter, 0)
	s.waitFor("Catalog service unavailable — press / to retry")
	s.key(tcell.KeyEscape, 0)
	s.keys("2")
	s.waitFor("25/?") // rows still listed from the backend DB, without catalog data
}
