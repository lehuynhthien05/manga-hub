package integration_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/stretchr/testify/assert"
	"mangahub/internal/tui/app"
)

func (s *screen) say(text string) {
	s.keys(text)
	s.key(tcell.KeyEnter, 0)
}

// Gate 9 steps 2–7 and 9: three real TUIs on the real chat hub.
func TestChat_RoomsAcrossTUIs(t *testing.T) {
	_, catAddr := serveCatalog(t, "127.0.0.1:0")
	h, _ := newBackend(t, catAddr, 2*time.Second, true)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	st := syncStack{url: srv.URL}
	for _, u := range []string{"alice", "bob", "charlie"} {
		st.user(t, u, "Passw0rdX")
	}
	code, body := call(t, "GET", srv.URL+"/ws", "", "")
	assert.Equal(t, http.StatusUnauthorized, code, "no upgrade without a token")
	assert.Contains(t, body, "unauthenticated")

	ws := app.Addrs{WS: "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"}
	a, b, c := startTUI(t, srv.URL, ws), startTUI(t, srv.URL, ws), startTUI(t, srv.URL, ws)
	for _, x := range []struct {
		s    *screen
		user string
		room string
	}{{a, "alice", "general"}, {b, "bob", "general"}, {c, "charlie", "random"}} {
		x.s.login(x.user, "Passw0rdX")
		x.s.waitFor("WS ●")
		x.s.keys("3")
		x.s.waitFor("Rooms")
		x.s.key(tcell.KeyTab, 0)
		x.s.say("/join " + x.room)
		x.s.waitFor("#" + x.room)
	}
	a.waitFor("bob joined")

	a.say("Chapter 26 is out!")
	b.waitFor("alice: Chapter 26 is out!")
	a.waitFor("alice: Chapter 26 is out!")
	b.say("[red]boom[-]")
	a.waitFor("bob: [red]boom[-]") // shown literally, not as a colour tag
	c.say("only random")
	c.waitFor("charlie: only random")

	a.say("/join Bad Room!")
	a.waitFor("Invalid room name")

	assert.NotContains(t, c.text(), "Chapter 26", "room isolation")
	assert.NotContains(t, a.text(), "only random", "room isolation")
	assert.NotContains(t, b.text(), "only random", "room isolation")

	b.a.Quit() // B goes away: a close frame, the hub unregisters and tells the room
	a.waitFor("bob left")
	a.say("still here")
	a.waitFor("alice: still here")
}
