package chatws

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mangahub/internal/auth"
	"mangahub/internal/testutil"
	"mangahub/pkg/protocol"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type env struct {
	hub *Hub
	url string
	mgr *auth.Manager
}

func start(t *testing.T, lim Limits) *env {
	t.Helper()
	gin.SetMode(gin.TestMode)
	mgr, err := auth.NewManager("test-secret-0123456789", time.Hour)
	require.NoError(t, err)
	hub := NewHub(lim, quiet)
	r := gin.New()
	r.GET("/ws", auth.Middleware(mgr, quiet), hub.Serve)
	srv := httptest.NewServer(r)
	t.Cleanup(func() { hub.Close(); srv.Close() })
	return &env{hub, "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws", mgr}
}

// peer reads in one goroutine into frames: gorilla read errors (deadlines too)
// are sticky, so "expect nothing" must not be a read deadline on the socket.
type peer struct {
	t      *testing.T
	conn   *websocket.Conn
	frames chan map[string]any
}

func newPeer(t *testing.T, conn *websocket.Conn) *peer {
	p := &peer{t, conn, make(chan map[string]any, 4096)}
	go func() {
		defer close(p.frames)
		for {
			var m map[string]any
			if conn.ReadJSON(&m) != nil {
				return
			}
			p.frames <- m
		}
	}()
	return p
}

func (e *env) dial(t *testing.T, user string) *peer {
	t.Helper()
	tok, _, _ := e.mgr.Issue("usr_"+user, user, "user")
	conn, resp, err := websocket.DefaultDialer.Dial(e.url, http.Header{"Authorization": {"Bearer " + tok}})
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	t.Cleanup(func() { _ = conn.Close() })
	return newPeer(t, conn)
}

func (p *peer) send(v any) { require.NoError(p.t, p.conn.WriteJSON(v)) }

func (p *peer) read() map[string]any {
	p.t.Helper()
	select {
	case m, ok := <-p.frames:
		require.True(p.t, ok, "connection closed")
		return m
	case <-time.After(2 * time.Second):
		p.t.Fatal("no frame within 2s")
		return nil
	}
}

// readType skips frames until one of type typ arrives.
func (p *peer) readType(typ string) map[string]any {
	p.t.Helper()
	for {
		if m := p.read(); m["type"] == typ {
			return m
		}
	}
}

func (p *peer) nothing() {
	p.t.Helper()
	select {
	case m := <-p.frames:
		p.t.Fatalf("expected nothing, got %v", m)
	case <-time.After(300 * time.Millisecond):
	}
}

// closed waits until the server ends the connection.
func (p *peer) closed() {
	p.t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-p.frames:
			if !ok {
				return
			}
		case <-deadline:
			p.t.Fatal("connection still open")
		}
	}
}

func (p *peer) join(room string) {
	p.t.Helper()
	p.send(protocol.WSJoin{Type: "join", Room: room})
	h := p.readType("history")
	assert.Equal(p.t, room, h["room"])
}

func chat(room, text string) protocol.WSChat {
	return protocol.WSChat{Type: "chat", Room: room, Text: text}
}

func TestWS_RoomChatAndHistory(t *testing.T) {
	e := start(t, DefaultLimits())
	alice, bob := e.dial(t, "alice"), e.dial(t, "bob")
	alice.join("general")
	bob.join("general")
	assert.Equal(t, "bob joined", alice.readType("system")["text"])

	alice.send(chat("general", "  Chapter 26 is out!  "))
	for _, p := range []*peer{alice, bob} { // the sender gets it too
		m := p.readType("chat")
		assert.Equal(t, "alice", m["user"])
		assert.Equal(t, "Chapter 26 is out!", m["text"], "trimmed")
		assert.NotZero(t, m["ts"])
	}

	carol := e.dial(t, "carol")
	carol.send(protocol.WSJoin{Type: "join", Room: "general"})
	h := carol.readType("history")
	require.Len(t, h["messages"], 1)
	assert.Equal(t, "Chapter 26 is out!", h["messages"].([]any)[0].(map[string]any)["text"])
}

func TestWS_RoomIsolationAndMembership(t *testing.T) {
	e := start(t, DefaultLimits())
	alice, bob, charlie := e.dial(t, "alice"), e.dial(t, "bob"), e.dial(t, "charlie")
	alice.join("general")
	bob.join("general")
	alice.readType("system")
	charlie.join("random")

	alice.send(chat("general", "hi general"))
	alice.readType("chat")
	bob.readType("chat")
	charlie.nothing()

	charlie.send(chat("random", "hi random"))
	charlie.readType("chat")
	alice.nothing()
	bob.nothing()

	charlie.send(chat("general", "sneaky")) // never joined general
	assert.Equal(t, "not a member of this room general", charlie.readType("error")["error"])
	alice.nothing()
	bob.nothing()
}

func TestWS_UpgradeAuth(t *testing.T) {
	e := start(t, DefaultLimits())
	for name, hdr := range map[string]http.Header{
		"no token": {}, "forged": {"Authorization": {"Bearer x.y.z"}},
	} {
		_, resp, err := websocket.DefaultDialer.Dial(e.url, hdr)
		require.ErrorIs(t, err, websocket.ErrBadHandshake, name)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, name)
		body, _ := io.ReadAll(resp.Body)
		assert.Contains(t, string(body), `"code":"unauthenticated"`, name)
	}
	e.dial(t, "alice") // a valid token upgrades (101 checked in dial)
}

func TestWS_AbruptDisconnect(t *testing.T) {
	e := start(t, DefaultLimits())
	alice, bob, charlie := e.dial(t, "alice"), e.dial(t, "bob"), e.dial(t, "charlie")
	alice.join("general")
	bob.join("general")
	alice.readType("system")
	alice.join("other")
	charlie.join("other")
	alice.readType("system")

	require.NoError(t, bob.conn.UnderlyingConn().Close()) // no close frame
	assert.Equal(t, "bob left", alice.readType("system")["text"])
	testutil.Eventually(t, func() bool { return e.hub.ClientCount() == 2 }, 2*time.Second, "bob unregistered")

	alice.send(chat("other", "still here"))
	assert.Equal(t, "still here", charlie.readType("chat")["text"])
}

func TestWS_MalformedInputIsolated(t *testing.T) {
	e := start(t, DefaultLimits())
	alice, bob := e.dial(t, "alice"), e.dial(t, "bob")
	alice.join("general")
	bob.join("general")
	alice.readType("system")

	for _, raw := range []string{`not json`, `{"type":"dance"}`, `{"type":"chat","room":"general","text":"   "}`,
		`{"type":"chat","room":"general","text":"` + strings.Repeat("a", 501) + `"}`, `{"type":"join","room":"Bad Room!"}`, `{"type":5}`} {
		require.NoError(t, alice.conn.WriteMessage(websocket.TextMessage, []byte(raw)))
		assert.NotEmpty(t, alice.readType("error")["error"], raw)
	}
	require.NoError(t, alice.conn.WriteMessage(websocket.BinaryMessage, []byte{1, 2}))
	assert.Equal(t, "binary frames are not supported", alice.readType("error")["error"])
	bob.nothing() // nothing leaked to the room

	// The rate limiter: of a 50-message burst only about the burst size (10) is broadcast.
	// Replies go through the sender's bounded send buffer without blocking, so some
	// "slow down" replies may be dropped: count what bob receives until the line goes quiet.
	for i := range 50 {
		alice.send(chat("general", fmt.Sprintf("m%d", i)))
	}
	chats := 0
	for quiet := false; !quiet; {
		select {
		case m, ok := <-bob.frames:
			if ok && m["type"] == "chat" {
				chats++
			}
			quiet = !ok
		case <-time.After(500 * time.Millisecond): // waiting for the absence of more frames
			quiet = true
		}
	}
	// Burst 10, minus the 2 tokens the invalid chats above already used, plus a little refill.
	assert.GreaterOrEqual(t, chats, 5, "the burst allowance gets through")
	assert.LessOrEqual(t, chats, 13, "the rest of the 50 is refused with 'slow down'")

	// An oversize frame closes only that connection.
	carol := e.dial(t, "carol")
	carol.join("general")
	_ = carol.conn.WriteMessage(websocket.TextMessage, []byte(strings.Repeat("x", 5000)))
	carol.closed()
	for bob.readType("system")["text"] != "carol left" { // skips "carol joined"
	}
}

// Hub level with writer-less clients: a loopback socket's buffers would hide a
// stalled reader, while a client whose send buffer nobody drains really is stuck.
func TestHub_StalledClientDoesNotBlock(t *testing.T) {
	lim := DefaultLimits()
	lim.SendBuffer, lim.MsgRate, lim.MsgBurst = 4, 1e6, 1e6
	h := NewHub(lim, quiet)
	mk := func(name string) *Client {
		c := &Client{hub: h, username: name, send: make(chan []byte, lim.SendBuffer), done: make(chan struct{}), rooms: map[string]struct{}{}}
		require.NoError(t, h.register(c))
		require.NoError(t, h.join(c, "flood"))
		return c
	}
	stalled, healthy, sender := mk("stalled"), mk("healthy"), mk("sender")
	go func() { // sender's own echoes must not fill up its buffer either
		for range sender.send {
		}
	}()
	got := 0
	for i := range 100 {
		require.NoError(t, h.chat(sender, "flood", fmt.Sprintf("m%d", i)))
		for len(healthy.send) > 0 { // the healthy client drains as it goes
			<-healthy.send
			got++
		}
	}
	assert.GreaterOrEqual(t, got, 100, "every message reached the healthy client")
	select {
	case <-stalled.done:
	default:
		t.Fatal("the stalled client was not closed")
	}
}

func TestWS_ConcurrentChatNoLeaks(t *testing.T) {
	before := runtime.NumGoroutine()
	lim := DefaultLimits()
	lim.MsgRate, lim.MsgBurst, lim.SendBuffer = 1e6, 1e6, 1024
	e := start(t, lim)
	rooms := []string{"a", "b", "c"}
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			tok, _, _ := e.mgr.Issue(fmt.Sprintf("u%d", i), fmt.Sprintf("u%d", i), "user")
			conn, _, err := websocket.DefaultDialer.Dial(e.url, http.Header{"Authorization": {"Bearer " + tok}})
			if !assert.NoError(t, err) {
				return
			}
			defer conn.Close()
			room := rooms[i%3]
			_ = conn.WriteJSON(protocol.WSJoin{Type: "join", Room: room})
			go func() { // drain until the connection ends
				for {
					if _, _, err := conn.ReadMessage(); err != nil {
						return
					}
				}
			}()
			for j := range 50 {
				_ = conn.WriteJSON(chat(room, fmt.Sprintf("%d-%d", i, j)))
				if i%5 == 0 && j == 25 { // some clients vanish mid-way
					_ = conn.UnderlyingConn().Close()
					return
				}
			}
		})
	}
	wg.Wait()
	e.hub.Close()
	testutil.Eventually(t, func() bool { return runtime.NumGoroutine() <= before+6 }, 3*time.Second,
		fmt.Sprintf("goroutines did not return to baseline (%d before)", before))
}

func TestCleanText(t *testing.T) {
	got, err := cleanText("  \x1b[31mred\x07 [red]boom[-] ", 500)
	require.NoError(t, err)
	assert.Equal(t, "[31mred [red]boom[-]", got, "control bytes go; tview tags stay text (the TUI escapes them)")
	_, err = cleanText("\n\t ", 500)
	assert.ErrorIs(t, err, ErrBadText)
}
