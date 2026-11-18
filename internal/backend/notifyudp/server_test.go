package notifyudp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mangahub/internal/auth"
	"mangahub/internal/clock"
	"mangahub/pkg/protocol"
)

type env struct {
	srv   *Server
	addr  net.Addr
	mgr   *auth.Manager
	clock *clock.Fake
}

func start(t *testing.T) *env {
	t.Helper()
	mgr, err := auth.NewManager("test-secret-0123456789", time.Hour)
	require.NoError(t, err)
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	fc := &clock.Fake{T: time.Now()}
	srv := NewServer(mgr, fc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = srv.Serve(ctx, pc); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return &env{srv, pc.LocalAddr(), mgr, fc}
}

// peer is one client socket, like `nc -u`.
type peer struct{ conn *net.UDPConn }

func (e *env) peer(t *testing.T) *peer {
	c, err := net.DialUDP("udp", nil, e.addr.(*net.UDPAddr))
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return &peer{c}
}

func (p *peer) send(t *testing.T, s string) {
	_, err := p.conn.Write([]byte(s))
	require.NoError(t, err)
}

func (p *peer) recv(t *testing.T) string {
	t.Helper()
	_ = p.conn.SetReadDeadline(time.Now().Add(time.Second))
	b := make([]byte, 2048)
	n, err := p.conn.Read(b)
	require.NoError(t, err)
	return string(b[:n])
}

func (p *peer) nothing(t *testing.T) {
	t.Helper()
	_ = p.conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	_, err := p.conn.Read(make([]byte, 2048))
	var ne net.Error
	require.ErrorAs(t, err, &ne)
	assert.True(t, ne.Timeout(), "expected silence, got %v", err)
}

func (e *env) subscribe(t *testing.T, p *peer, user string) {
	t.Helper()
	tok, _, _ := e.mgr.Issue(user, user, "user")
	p.send(t, `{"type":"subscribe","token":"`+tok+`"}`)
	assert.JSONEq(t, `{"type":"subscribed"}`, p.recv(t))
}

var ev = protocol.NewChapter{Type: protocol.TypeNewChapter, MangaID: "one-piece", Title: "One Piece", Chapter: 1101}

func TestUDP_BroadcastToSubscribersOnly(t *testing.T) {
	e := start(t)
	s1, s2, s3 := e.peer(t), e.peer(t), e.peer(t)
	e.subscribe(t, s1, "alice")
	e.subscribe(t, s2, "bob")

	assert.Equal(t, 2, e.srv.Notify(ev))
	want := `{"type":"new_chapter","manga_id":"one-piece","title":"One Piece","chapter":1101}`
	assert.JSONEq(t, want, s1.recv(t))
	assert.JSONEq(t, want, s2.recv(t))
	s3.nothing(t) // never subscribed

	s1.send(t, `{"type":"ping"}`)
	assert.JSONEq(t, `{"type":"pong"}`, s1.recv(t))
	s3.send(t, `{"type":"ping"}`)
	s3.nothing(t) // unknown sender: no reply
}

func TestUDP_UnsubscribeAndBadToken(t *testing.T) {
	e := start(t)
	p := e.peer(t)
	e.subscribe(t, p, "alice")
	tok, _, _ := e.mgr.Issue("alice", "alice", "user")
	p.send(t, `{"type":"unsubscribe","token":"`+tok+`"}`)
	assert.JSONEq(t, `{"type":"unsubscribed"}`, p.recv(t))
	assert.Equal(t, 0, e.srv.Notify(ev))
	p.nothing(t)

	bad := e.peer(t)
	bad.send(t, `{"type":"subscribe","token":"x.y.z"}`)
	assert.JSONEq(t, `{"type":"error","error":"invalid token"}`, bad.recv(t))
	assert.Equal(t, 0, e.srv.Count())
}

func TestUDP_ClosedReceiverDoesNotBreakBroadcast(t *testing.T) {
	e := start(t)
	s1, s2 := e.peer(t), e.peer(t)
	e.subscribe(t, s1, "alice")
	e.subscribe(t, s2, "alice")
	require.NoError(t, s1.conn.Close())

	sent := e.srv.Notify(ev)
	assert.GreaterOrEqual(t, sent, 1, "the closed port usually still counts: sent = attempted, not delivered")
	assert.Contains(t, s2.recv(t), "new_chapter")
	e.subscribe(t, e.peer(t), "carol") // Serve still answers
}

func TestUDP_GarbageIgnored(t *testing.T) {
	e := start(t)
	p := e.peer(t)
	for _, g := range []string{"\x00\x01garbage", `{}`, `{"type":"x"}`, `not json`, strings.Repeat("a", 4096)} {
		p.send(t, g)
	}
	p.nothing(t)
	e.subscribe(t, p, "alice") // the server is still healthy
}

func TestUDP_TTLEviction(t *testing.T) {
	e := start(t)
	p := e.peer(t)
	e.subscribe(t, p, "alice")
	e.clock.Advance(TTL - time.Second)
	assert.Zero(t, e.srv.evictExpired())
	e.clock.Advance(2 * time.Second)
	assert.Equal(t, 1, e.srv.evictExpired())
	assert.Equal(t, 0, e.srv.Notify(ev))
}

func TestUDP_NotifyBeforeServe(t *testing.T) {
	assert.Equal(t, 0, NewServer(nil, clock.System{}, slog.New(slog.NewTextHandler(io.Discard, nil))).Notify(ev))
}

func TestUDP_DatagramIsSmall(t *testing.T) {
	b, _ := json.Marshal(protocol.NewChapter{Type: "new_chapter", MangaID: strings.Repeat("a", 64), Title: strings.Repeat("ワ", 60), Chapter: 99999})
	assert.Less(t, len(b), 512)
}

// Stress (-short skips it): 20 sockets subscribe while 20 goroutines Notify; -race stays quiet.
func TestStress_UDPSubscribeWhileNotify(t *testing.T) {
	if testing.Short() {
		t.Skip("stress")
	}
	e := start(t)
	var wg sync.WaitGroup
	for i := range 20 {
		p := e.peer(t)
		wg.Go(func() {
			tok, _, _ := e.mgr.Issue(fmt.Sprintf("u%d", i), "u", "user")
			p.send(t, `{"type":"subscribe","token":"`+tok+`"}`)
			// A Notify may arrive before "subscribed": the address is registered just
			// before the reply is sent (and UDP promises no ordering anyway).
			for !strings.Contains(p.recv(t), `"subscribed"`) {
			}
		})
		wg.Go(func() {
			for range 20 {
				e.srv.Notify(ev)
			}
		})
	}
	wg.Wait()
	assert.Equal(t, 20, e.srv.Count())
	assert.Equal(t, 20, e.srv.Notify(ev))
}
