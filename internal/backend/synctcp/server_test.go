package synctcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mangahub/internal/auth"
	"mangahub/internal/testutil"
	"mangahub/pkg/protocol"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type env struct {
	addr  string
	srv   *Server
	mgr   *auth.Manager
	stop  context.CancelFunc
	ended chan error
}

func start(t *testing.T, lim Limits) *env {
	t.Helper()
	mgr, err := auth.NewManager("test-secret-0123456789", time.Hour)
	require.NoError(t, err)
	ln := testutil.ListenLoopback(t)
	ctx, cancel := context.WithCancel(context.Background())
	e := &env{addr: ln.Addr().String(), srv: NewServer(mgr, lim, quiet), mgr: mgr, stop: cancel, ended: make(chan error, 1)}
	go func() { e.ended <- e.srv.Serve(ctx, ln) }()
	t.Cleanup(func() { cancel(); <-e.ended })
	return e
}

func (e *env) token(t *testing.T, user string) string {
	tok, _, err := e.mgr.Issue(user, user, protocol.RoleUser)
	require.NoError(t, err)
	return tok
}

type client struct {
	conn net.Conn
	r    *bufio.Reader
	id   string
}

func (e *env) dialAuthed(t *testing.T, user string) *client {
	t.Helper()
	conn, err := net.Dial("tcp", e.addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	_, err = conn.Write([]byte(`{"type":"auth","token":"` + e.token(t, user) + `"}` + "\n"))
	require.NoError(t, err)
	c := &client{conn: conn, r: bufio.NewReader(conn)}
	var ok protocol.TCPAuthOK
	require.NoError(t, json.Unmarshal(c.read(t), &ok))
	require.Equal(t, protocol.TypeAuthOK, ok.Type)
	require.True(t, strings.HasPrefix(ok.SessionID, "s_"))
	c.id = ok.SessionID
	return c
}

func (c *client) read(t *testing.T) []byte {
	t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := c.r.ReadBytes('\n')
	require.NoError(t, err)
	return line
}

func (c *client) event(t *testing.T) protocol.ProgressUpdated {
	t.Helper()
	var ev protocol.ProgressUpdated
	require.NoError(t, json.Unmarshal(c.read(t), &ev))
	return ev
}

func ev(v int) protocol.ProgressUpdated {
	return protocol.ProgressUpdated{Type: protocol.TypeProgressUpdated, MangaID: "one-piece", Chapter: v, Version: v, Status: "reading"}
}

func TestTCP_FramingMergedAndSplit(t *testing.T) {
	e := start(t, DefaultLimits())
	c := e.dialAuthed(t, "alice")

	_, _ = c.conn.Write([]byte("{\"type\":\"ping\"}\n{\"type\":\"ping\"}\n")) // two messages in one write
	assert.JSONEq(t, `{"type":"pong"}`, string(c.read(t)))
	assert.JSONEq(t, `{"type":"pong"}`, string(c.read(t)))

	_, _ = c.conn.Write([]byte(`{"type":`)) // one message split across two writes
	time.Sleep(50 * time.Millisecond)       // not synchronisation: forces two separate TCP segments
	_, _ = c.conn.Write([]byte(`"ping"}` + "\n"))
	assert.JSONEq(t, `{"type":"pong"}`, string(c.read(t)))
	testutil.ExpectNoData(t, c.conn, 200*time.Millisecond)

	// A 70 KiB first line without a newline: error line, then close.
	conn, err := net.Dial("tcp", e.addr)
	require.NoError(t, err)
	defer conn.Close()
	_, _ = conn.Write([]byte(strings.Repeat("x", 70<<10)))
	assert.JSONEq(t, `{"type":"error","error":"line too long"}`, string(testutil.ReadLine(t, conn, 2*time.Second)))
}

func TestTCP_AuthFailures(t *testing.T) {
	lim := DefaultLimits()
	lim.AuthTimeout = 200 * time.Millisecond
	e := start(t, lim)
	expired, _ := auth.NewManager("test-secret-0123456789", -time.Minute)
	expTok, _, _ := expired.Issue("alice", "alice", "user")

	for name, tc := range map[string]struct{ send, want string }{
		"silence":       {"", "expected auth message"},
		"garbage":       {"hello\n", "expected auth message"},
		"wrong type":    {`{"type":"ping"}` + "\n", "expected auth message"},
		"invalid token": {`{"type":"auth","token":"x.y.z"}` + "\n", "invalid token"},
		"expired token": {`{"type":"auth","token":"` + expTok + `"}` + "\n", "invalid token"},
	} {
		conn, err := net.Dial("tcp", e.addr)
		require.NoError(t, err)
		if tc.send != "" {
			_, _ = conn.Write([]byte(tc.send))
		}
		r := bufio.NewReader(conn)
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		line, err := r.ReadBytes('\n')
		require.NoError(t, err, name)
		assert.JSONEq(t, `{"type":"error","error":"`+tc.want+`"}`, string(line), name)
		_, err = r.ReadByte()
		assert.ErrorIs(t, err, io.EOF, name+": the server closes the connection")
		_ = conn.Close()
	}
	assert.Zero(t, e.srv.Hub().Count())
}

func TestTCP_SameUserOnly(t *testing.T) {
	e := start(t, DefaultLimits())
	a1, a2, b1 := e.dialAuthed(t, "alice"), e.dialAuthed(t, "alice"), e.dialAuthed(t, "bob")
	testutil.Eventually(t, func() bool { return e.srv.Hub().Count() == 3 }, time.Second, "3 sessions")

	e.srv.Hub().Publish("alice", ev(30), a1.id) // origin a1 excluded
	assert.Equal(t, ev(30), a2.event(t))
	testutil.ExpectNoData(t, a1.conn, 300*time.Millisecond)
	testutil.ExpectNoData(t, b1.conn, 300*time.Millisecond)

	e.srv.Hub().Publish("alice", ev(31), "")
	assert.Equal(t, 31, a1.event(t).Chapter)
	assert.Equal(t, 31, a2.event(t).Chapter)
	e.srv.Hub().Publish("alice", ev(32), b1.id) // a foreign session id excludes nobody of alice's
	assert.Equal(t, 32, a1.event(t).Chapter)
	assert.Equal(t, 32, a2.event(t).Chapter)
	testutil.ExpectNoData(t, b1.conn, 300*time.Millisecond)
}

func TestTCP_AbruptDisconnectAndLimits(t *testing.T) {
	lim := DefaultLimits()
	lim.MaxPerUser = 2
	e := start(t, lim)
	a1, a2 := e.dialAuthed(t, "alice"), e.dialAuthed(t, "alice")

	conn, err := net.Dial("tcp", e.addr) // the third session of alice is refused
	require.NoError(t, err)
	defer conn.Close()
	_, _ = conn.Write([]byte(`{"type":"auth","token":"` + e.token(t, "alice") + `"}` + "\n"))
	assert.JSONEq(t, `{"type":"error","error":"too many connections"}`, string(testutil.ReadLine(t, conn, 2*time.Second)))

	_ = a1.conn.Close() // no goodbye
	testutil.Eventually(t, func() bool { return e.srv.Hub().Count() == 1 }, 2*time.Second, "a1 removed")
	e.srv.Hub().Publish("alice", ev(7), "")
	assert.Equal(t, 7, a2.event(t).Chapter)
}

func TestTCP_IdleTimeoutClosesSilentPeer(t *testing.T) {
	lim := DefaultLimits()
	lim.IdleTimeout = 200 * time.Millisecond
	e := start(t, lim)
	c := e.dialAuthed(t, "alice")
	_ = c.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err := c.r.ReadByte()
	assert.ErrorIs(t, err, io.EOF, "silence longer than IdleTimeout closes the session")
	testutil.Eventually(t, func() bool { return e.srv.Hub().Count() == 0 }, time.Second, "removed")
}

func TestTCP_ConcurrentPublishAndNoLeaks(t *testing.T) {
	before := runtime.NumGoroutine()
	lim := DefaultLimits()
	lim.SendBuffer, lim.MaxPerUser = 256, 20
	e := start(t, lim)
	const sessions, events = 20, 100
	clients := make([]*client, sessions)
	for i := range clients {
		clients[i] = e.dialAuthed(t, "alice")
	}
	testutil.Eventually(t, func() bool { return e.srv.Hub().Count() == sessions }, 2*time.Second, "all registered")

	var mu sync.Mutex // publishers take turns so versions are published in order
	next := 1
	var pub sync.WaitGroup
	for range 4 {
		pub.Go(func() {
			for {
				mu.Lock()
				if next > events {
					mu.Unlock()
					return
				}
				e.srv.Hub().Publish("alice", ev(next), "")
				next++
				mu.Unlock()
			}
		})
	}
	var recv sync.WaitGroup
	for _, c := range clients {
		recv.Go(func() {
			for v := 1; v <= events; v++ {
				assert.Equal(t, v, c.event(t).Version)
			}
		})
	}
	pub.Wait()
	recv.Wait()

	e.stop()
	require.NoError(t, <-e.ended)
	e.ended <- nil // for the cleanup
	for _, c := range clients {
		_ = c.conn.Close()
	}
	testutil.Eventually(t, func() bool { return runtime.NumGoroutine() <= before+2 }, 2*time.Second,
		"goroutines leaked after Serve returned")
}

// net.Pipe has no kernel buffer: a peer that never reads really blocks the writer,
// which a loopback socket (hundreds of KiB of buffer) would hide.
func TestHub_SlowConsumerDoesNotBlock(t *testing.T) {
	lim := DefaultLimits()
	lim.SendBuffer = 4
	h := NewHub(lim, quiet)
	pipe := func() (*session, net.Conn) {
		srvEnd, cliEnd := net.Pipe()
		t.Cleanup(func() { _ = cliEnd.Close() })
		s := newSession(srvEnd, "alice", lim.SendBuffer, quiet)
		require.NoError(t, h.add(s))
		go s.writeLoop(lim.WriteTimeout)
		t.Cleanup(func() { s.close("test end") })
		return s, cliEnd
	}
	slow, _ := pipe() // never read
	_, healthyConn := pipe()
	got := make(chan int, 100)
	go func() {
		r := bufio.NewReader(healthyConn)
		for range 100 {
			line, err := r.ReadBytes('\n')
			var ev protocol.ProgressUpdated
			if err != nil || json.Unmarshal(line, &ev) != nil {
				return
			}
			got <- ev.Version
		}
	}()

	begin := time.Now()
	for v := 1; v <= 100; v++ {
		h.Publish("alice", ev(v), "")
		// Pace the producer to the healthy reader (its buffer is only 4 too).
		testutil.Eventually(t, func() bool { return len(got) >= v }, 2*time.Second, "healthy session fell behind")
	}
	assert.Less(t, time.Since(begin), 3*time.Second, "Publish never waits for the slow session")
	select {
	case <-slow.done:
	default:
		t.Fatal("the slow session was not closed")
	}
	for v := 1; v <= 100; v++ {
		assert.Equal(t, v, <-got)
	}
}
