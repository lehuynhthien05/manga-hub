package client_test

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mangahub/internal/testutil"
	"mangahub/internal/tui/client"
	"mangahub/pkg/protocol"
)

type recorder struct {
	mu     sync.Mutex
	ids    []string
	events []protocol.ProgressUpdated
}

func (r *recorder) state(s, id string, _ error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s == client.StateConnected {
		r.ids = append(r.ids, id)
	}
}

func (r *recorder) event(ev protocol.ProgressUpdated) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recorder) snapshot() ([]string, []protocol.ProgressUpdated) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.ids...), append([]protocol.ProgressUpdated{}, r.events...)
}

// fakeHub accepts on ln, expects the auth line with token "tok", answers auth_ok
// with a fresh session id, sends one event, and keeps the connection open.
// (The real hub is exercised in internal/integration; client must not import the backend.)
func fakeHub(ln net.Listener, gen int) {
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				line, err := bufio.NewReader(c).ReadString('\n')
				if err != nil || !strings.Contains(line, `"token":"tok"`) {
					_, _ = c.Write([]byte(`{"type":"error","error":"invalid token"}` + "\n"))
					return
				}
				fmt.Fprintf(c, `{"type":"auth_ok","session_id":"s_gen%d"}`+"\n", gen)
				fmt.Fprintf(c, `{"type":"pong"}`+"\n"+`{"type":"progress_updated","manga_id":"one-piece","chapter":%d,"version":%d}`+"\n", gen, gen)
				_, _ = c.Read(make([]byte, 1)) // hold until the peer goes away
			}()
		}
	}()
}

func TestSyncClient_ReceivesAndReconnects(t *testing.T) {
	ln := testutil.ListenLoopback(t)
	addr := ln.Addr().String()
	fakeHub(ln, 1)

	rec := &recorder{}
	c := &client.SyncClient{Addr: addr, Token: "tok", OnState: rec.state, OnEvent: rec.event,
		MinBackoff: 20 * time.Millisecond, MaxBackoff: 100 * time.Millisecond, ReadTimeout: 300 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	testutil.Eventually(t, func() bool { _, evs := rec.snapshot(); return len(evs) == 1 }, 2*time.Second, "first event")
	assert.Equal(t, "s_gen1", c.SessionID())

	// The "backend" goes away and comes back on the same port: the client reconnects by
	// itself. The fake never answers pings, so the short ReadTimeout drops the old link.
	require.NoError(t, ln.Close())
	testutil.Eventually(t, func() bool { return c.SessionID() == "" }, 2*time.Second, "noticed the dead link")
	ln2, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln2.Close() })
	fakeHub(ln2, 2)

	testutil.Eventually(t, func() bool { return c.SessionID() == "s_gen2" }, 3*time.Second, "reconnected with a new session id")
	ids, evs := rec.snapshot()
	assert.Equal(t, "s_gen1", ids[0])
	assert.Equal(t, "s_gen2", ids[len(ids)-1])
	assert.Equal(t, 2, evs[len(evs)-1].Chapter)
}

func TestSyncClient_RejectedTokenKeepsRetrying(t *testing.T) {
	ln := testutil.ListenLoopback(t)
	fakeHub(ln, 1)
	var mu sync.Mutex
	var errs []error
	c := &client.SyncClient{Addr: ln.Addr().String(), Token: "bad", OnEvent: func(protocol.ProgressUpdated) {},
		OnState: func(s, _ string, err error) {
			if s == client.StateDisconnected {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}, MinBackoff: 10 * time.Millisecond, MaxBackoff: 20 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	testutil.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(errs) >= 2 }, 2*time.Second, "retries")
	cancel()
	<-done
	assert.Empty(t, c.SessionID())
	mu.Lock()
	assert.ErrorContains(t, errs[0], "invalid token")
	mu.Unlock()
}
