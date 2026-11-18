package client_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mangahub/internal/testutil"
	"mangahub/internal/tui/client"
	"mangahub/pkg/protocol"
)

// fakeChat echoes every frame back and records joins; it rejects a bad token with 401.
type fakeChat struct {
	mu    sync.Mutex
	joins []string
	conns []*websocket.Conn
}

func (f *fakeChat) handler(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer tok" {
		http.Error(w, `{"error":"invalid or expired token","code":"unauthenticated"}`, http.StatusUnauthorized)
		return
	}
	conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
	if err != nil {
		return
	}
	f.mu.Lock()
	f.conns = append(f.conns, conn)
	f.mu.Unlock()
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if strings.Contains(string(data), `"type":"join"`) {
			f.mu.Lock()
			f.joins = append(f.joins, string(data))
			f.mu.Unlock()
		}
		_ = conn.WriteMessage(websocket.TextMessage, data)
	}
}

func (f *fakeChat) joinCount() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.joins) }

func TestChatClient_SendReceiveRejoin(t *testing.T) {
	f := &fakeChat{}
	ln := testutil.ListenLoopback(t)
	addr := ln.Addr().String()
	srv := &http.Server{Handler: http.HandlerFunc(f.handler)}
	go func() { _ = srv.Serve(ln) }()

	var mu sync.Mutex
	var frames []string
	var states []string
	c := &client.ChatClient{URL: "ws://" + addr + "/ws", Token: "tok", MinBackoff: 20 * time.Millisecond, MaxBackoff: 50 * time.Millisecond,
		OnFrame: func(b []byte) { mu.Lock(); frames = append(frames, string(b)); mu.Unlock() },
		OnState: func(s string, _ error) { mu.Lock(); states = append(states, s); mu.Unlock() }}
	assert.False(t, c.Send(protocol.WSChat{Type: "chat", Room: "general", Text: "x"}), "not connected yet")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	testutil.Eventually(t, c.Connected, 2*time.Second, "connected")
	require.True(t, c.Join("general"))
	require.True(t, c.Send(protocol.WSChat{Type: "chat", Room: "general", Text: "hello"}))
	testutil.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(frames) == 2 }, 2*time.Second, "echoes")
	assert.Contains(t, frames[1], `"text":"hello"`)

	// The server goes away (abruptly) and comes back on the same port: the client rejoins.
	_ = srv.Close()
	f.mu.Lock()
	for _, c := range f.conns {
		_ = c.UnderlyingConn().Close()
	}
	f.mu.Unlock()
	testutil.Eventually(t, func() bool { return !c.Connected() }, 2*time.Second, "noticed the drop")
	ln2, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	srv2 := &http.Server{Handler: http.HandlerFunc(f.handler)}
	go func() { _ = srv2.Serve(ln2) }()
	t.Cleanup(func() { _ = srv2.Close() })
	testutil.Eventually(t, func() bool { return f.joinCount() == 2 }, 3*time.Second, "re-joined general after reconnect")
}

func TestChatClient_BadTokenRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc((&fakeChat{}).handler))
	t.Cleanup(srv.Close)
	var mu sync.Mutex
	var errs []error
	c := &client.ChatClient{URL: "ws" + strings.TrimPrefix(srv.URL, "http"), Token: "bad", MinBackoff: 10 * time.Millisecond,
		OnFrame: func([]byte) {}, OnState: func(s string, err error) {
			if s == client.StateDisconnected {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	testutil.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(errs) >= 2 }, 2*time.Second, "retries")
	cancel()
	<-done
	mu.Lock()
	assert.ErrorIs(t, errs[0], websocket.ErrBadHandshake)
	mu.Unlock()
}
